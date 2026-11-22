package gems

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/gemsdb"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Store is what a scan needs from the stored listings.
type Store interface {
	// StoreScanned keeps an ad without announcing it.
	StoreScanned(ctx context.Context, l events.ListingDiscovered) error
	// UnseenSince lists active ads not seen since since (Source, ExternalID,
	// Feed and Location only).
	UnseenSince(ctx context.Context, source string, since time.Time) ([]events.ListingDiscovered, error)
	MarkGoneIDs(ctx context.Context, source string, ids []string) (int64, error)
	// Stored lists every stored ad of source with a price whose Location
	// starts with location, ad-page details merged.
	Stored(ctx context.Context, source, location string) ([]Item, error)
	SaveDetails(ctx context.Context, before, enriched events.ListingDiscovered) error
}

// Scanner runs the scans admins request, one at a time. A full scan has
// two passes: it reads every listing of the category's feed and rates each
// from what the list shows (saved at once, so the finder is usable), then
// reads the ad page of every potential buy not read before (the category's
// Candidate) and rates everything again. A recalculation (Rate) skips the
// list pages: the stored listings are rated, then pass 2 as above.
type Scanner struct {
	Service *Service
	Store   Store
	Sources sources.Registry
	// Wait spaces a source's requests; shared with its regular scraping,
	// so a scan does not double the load on the site.
	Wait func(ctx context.Context, source string) bool
	// DetailGap is an extra pause before each ad page, on top of Wait,
	// stretched by up to half again at random.
	DetailGap time.Duration
	// Sleep pauses; nil is a real sleep. Tests skip the waiting.
	Sleep func(ctx context.Context, d time.Duration) bool
	// MaxPages caps a scan; one that reaches it marks nothing sold.
	MaxPages  int
	PollEvery time.Duration
}

// Pass 2's manners: a longer break (20–40 s) and a save (re-rating
// everything) every 100 ad pages, and on a site pushing back (429, 403,
// 5xx) a pause of 1, 2, 4 minutes; the third push without a good read
// between ends the pass, as do 10 failed reads without one.
const (
	breakEvery   = 100
	breakFor     = 20 * time.Second // to 40 s
	saveEvery    = 100
	pushbackWait = time.Minute
	maxPushbacks = 3
	maxFailures  = 10
)

// scannable are the sources that can scan at least one of the service's
// categories.
func (s *Scanner) scannable() []string {
	var names []string
	for _, n := range s.Sources.Names() {
		sc, ok := s.Sources[n].(sources.Scannable)
		if !ok {
			continue
		}
		if slices.ContainsFunc(s.Service.cats, func(c Category) bool { _, ok := sc.ScanScope(c.ID()); return ok }) {
			names = append(names, n)
		}
	}
	return names
}

// Run looks for requested scans until ctx is done. Scans left running by a
// previous run (the scraper restarted) are marked failed first.
func (s *Scanner) Run(ctx context.Context) {
	names := s.scannable()
	if len(names) == 0 {
		return
	}
	if n, err := s.Service.q.InterruptScans(ctx, names); err != nil {
		slog.Error("mark interrupted gem scans", "err", err)
	} else if n > 0 {
		slog.Info("gem scans interrupted by a restart", "scans", n)
	}
	for {
		r, err := s.Service.q.ClaimScan(ctx, names)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			if ctx.Err() == nil {
				slog.Error("claim gem scan", "err", err)
			}
		default:
			s.run(ctx, scanOf(r))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.PollEvery):
		}
	}
}

// outcome is what a scan did, for its record.
type outcome struct {
	gone, rated int
	complete    bool
	notes       []string // why it was not complete, or what it skipped
}

func (s *Scanner) run(ctx context.Context, scan Scan) {
	slog.Info("gem scan started", "id", scan.ID, "source", scan.Source, "category", scan.Category)
	out, err := s.scan(ctx, scan)
	status, msg := "done", ""
	note := strings.Join(out.notes, "; ")
	if err != nil {
		status, msg = "failed", err.Error()
		slog.Error("gem scan failed", "id", scan.ID, "err", err)
	} else {
		slog.Info("gem scan done", "id", scan.ID, "gone", out.gone, "rated", out.rated, "complete", out.complete, "note", note)
	}
	// A fresh context: a scan cut short by shutdown still records it.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Service.q.FinishScan(fctx, gemsdb.FinishScanParams{
		ID: scan.ID, Status: status, Gone: int32(out.gone), Rated: int32(out.rated), Error: msg,
		Complete: out.complete, Note: note,
	}); err != nil {
		slog.Error("finish gem scan", "id", scan.ID, "err", err)
	}
}

func (s *Scanner) scan(ctx context.Context, scan Scan) (outcome, error) {
	var out outcome
	cat, ok := s.Service.Category(scan.Category)
	if !ok {
		return out, fmt.Errorf("unknown category %q", scan.Category)
	}
	src, err := s.Sources.Get(scan.Source)
	if err != nil {
		return out, err
	}
	sc, ok := src.(sources.Scannable)
	var scope sources.ScanScope
	if ok {
		scope, ok = sc.ScanScope(cat.ID())
	}
	if !ok {
		return out, fmt.Errorf("%s cannot be scanned for %s", scan.Source, cat.ID())
	}

	// Pass 1: every listing, rated from the list page. A recalculation
	// rates the stored ones as they are.
	if scan.Mode != Rate {
		if err := s.relist(ctx, scan, src, cat, scope, &out); err != nil {
			return out, err
		}
	}
	items, err := s.Store.Stored(ctx, scan.Source, scope.Location)
	if err != nil {
		return out, fmt.Errorf("stored listings: %w", err)
	}
	r, ok := cat.Rater(src, items)
	if !ok {
		return out, fmt.Errorf("%s cannot be rated for %s", scan.Source, cat.ID())
	}
	results := r.Rate()
	if err := s.save(ctx, scan, items, results); err != nil {
		return out, err
	}
	out.rated = len(results)

	// Pass 2: the potential buys' ad pages, then everything rated again
	// (what the others are compared with changed too).
	gone, note, err := s.detail(ctx, scan, src, items, r, results)
	out.gone += gone
	if note != "" {
		out.notes = append(out.notes, note)
	}
	// Cut short by shutdown, what was read is still saved.
	sctx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		sctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
	}
	results = r.Rate()
	if serr := s.save(sctx, scan, items, results); serr != nil {
		return out, serr
	}
	out.rated = len(results)
	return out, err
}

// relist reads every list page of the category again and marks sold the
// listings it no longer has.
func (s *Scanner) relist(ctx context.Context, scan Scan, src sources.Source, cat Category, scope sources.ScanScope, out *outcome) error {
	ads, complete, err := s.crawl(ctx, scan, src, scope.Feed)
	if err != nil {
		return err
	}
	if !complete {
		out.notes = append(out.notes, fmt.Sprintf("stopped after %d pages before the end; sold ones not updated", s.MaxPages))
		return nil
	}
	if scan.StartedAt == nil {
		return nil
	}
	var note string
	out.gone, note, err = s.markGone(ctx, src, cat, *scan.StartedAt, ads)
	if err != nil {
		return err
	}
	out.complete = note == ""
	if note != "" {
		out.notes = append(out.notes, note)
	}
	return nil
}

// errEmptyStart: the feed's first page listed nothing. A block or a layout
// change looks like that; it is never a real end.
var errEmptyStart = errors.New("the first page listed nothing; the site may be blocking us or have changed")

// crawl reads every page of feed, storing what it finds, and returns how
// many ads it saw. complete is false when it stopped at MaxPages rather than
// at the end of the feed: past the last page a site shows nothing, or the
// last page again.
func (s *Scanner) crawl(ctx context.Context, scan Scan, src sources.Source, feed string) (ads int, complete bool, err error) {
	seen := map[string]bool{}
	for page := 1; page <= s.MaxPages; page++ {
		if !s.Wait(ctx, scan.Source) {
			return ads, false, ctx.Err()
		}
		ls, err := src.FetchFeed(ctx, feed, page)
		if err != nil {
			return ads, false, fmt.Errorf("page %d: %w", page, err)
		}
		if len(ls) == 0 && page == 1 {
			return 0, false, errEmptyStart
		}
		fresh := 0
		for _, l := range ls {
			if seen[l.ExternalID] {
				continue
			}
			seen[l.ExternalID] = true
			fresh++
			l.Feed = feed
			if err := s.Store.StoreScanned(ctx, l); err != nil {
				return ads, false, fmt.Errorf("store %s: %w", l.ExternalID, err)
			}
		}
		ads += fresh
		if err := s.Service.q.ScanProgress(ctx, gemsdb.ScanProgressParams{ID: scan.ID, Pages: int32(page), Ads: int32(ads)}); err != nil {
			return ads, false, err
		}
		if fresh == 0 {
			return ads, true, nil
		}
	}
	return ads, false, nil
}

// maxGoneShare: if more of the stored listings than this went missing in
// one scan, the crawl more likely broke off (an empty page mid-feed) than
// half the market sold, so nothing is marked.
const maxGoneShare = 0.5

// markGone marks sold the category's listings the scan did not see; note
// says so when it refused to.
func (s *Scanner) markGone(ctx context.Context, src sources.Source, cat Category, since time.Time, ads int) (int, string, error) {
	unseen, err := s.Store.UnseenSince(ctx, src.Name(), since)
	if err != nil {
		return 0, "", fmt.Errorf("unseen: %w", err)
	}
	var ids []string
	for _, l := range unseen {
		if cat.Belongs(src, l) {
			ids = append(ids, l.ExternalID)
		}
	}
	if len(ids) == 0 {
		return 0, "", nil
	}
	if share := float64(len(ids)) / float64(len(ids)+ads); share > maxGoneShare {
		return 0, fmt.Sprintf("%d of %d stored listings were missing, too many to be sold; sold ones not updated", len(ids), len(ids)+ads), nil
	}
	n, err := s.Store.MarkGoneIDs(ctx, src.Name(), ids)
	return int(n), "", err
}

// detail reads the ad page of every potential buy among results not read
// before, best first, paced by Wait plus DetailGap and backing off when the
// site pushes back. It saves the re-rated listings every saveEvery pages,
// so progress shows. A removed ad is marked sold. note says why it stopped
// early; err is only ctx's or a failed save.
func (s *Scanner) detail(ctx context.Context, scan Scan, src sources.Source, items []Item, r Rater, results []Rated) (gone int, note string, err error) {
	enricher, ok := src.(sources.Enricher)
	if !ok {
		return 0, "", nil
	}
	var todo []int
	for _, x := range results {
		if x.Candidate && !items[x.Item].Detailed {
			todo = append(todo, x.Item)
		}
	}
	progress := func(done int) error {
		return s.Service.q.DetailProgress(ctx, gemsdb.DetailProgressParams{ID: scan.ID, DetailTotal: int32(len(todo)), Detailed: int32(done)})
	}
	if err := progress(0); err != nil || len(todo) == 0 {
		return 0, "", err
	}
	slog.Info("gem scan: reading ad pages", "id", scan.ID, "candidates", len(todo))

	var removed []string
	markRemoved := func() {
		if len(removed) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		n, err := s.Store.MarkGoneIDs(fctx, src.Name(), removed)
		if err != nil {
			slog.Warn("gem scan: mark removed", "err", err)
		}
		gone += int(n)
		removed = nil
	}
	defer markRemoved()

	read, pushbacks, failures := 0, 0, 0
	for n, i := range todo {
		if n > 0 && n%breakEvery == 0 && !s.sleep(ctx, jittered(breakFor, 1)) {
			return gone, "", ctx.Err()
		}
		if !s.sleep(ctx, jittered(s.DetailGap, 0.5)) || !s.Wait(ctx, src.Name()) {
			return gone, "", ctx.Err()
		}
		l := items[i].Listing
		enriched, err := enricher.Enrich(ctx, l)
		switch {
		case ctx.Err() != nil:
			return gone, "", ctx.Err()
		case errors.Is(err, sources.ErrGone):
			pushbacks, failures = 0, 0
			removed = append(removed, l.ExternalID)
			items[i].Active = false
			r.Update(i, items[i])
		case pushedBack(err):
			pushbacks++
			slog.Warn("gem scan: the site pushed back", "id", l.ExternalID, "err", err, "times", pushbacks)
			if pushbacks >= maxPushbacks {
				return gone, fmt.Sprintf("%s pushed back (%v); read %d of %d ad pages, the rest next scan", src.Name(), err, read, len(todo)), nil
			}
			if !s.sleep(ctx, pushbackWait<<(pushbacks-1)) {
				return gone, "", ctx.Err()
			}
		case err != nil:
			failures++
			slog.Warn("gem scan: ad page unavailable", "id", l.ExternalID, "err", err)
			if failures >= maxFailures {
				return gone, fmt.Sprintf("%d ad pages in a row failed (%v); read %d of %d, the rest next scan", failures, err, read, len(todo)), nil
			}
		default:
			pushbacks, failures = 0, 0
			if err := s.Store.SaveDetails(ctx, l, enriched); err != nil {
				slog.Warn("gem scan: save ad page", "id", l.ExternalID, "err", err)
			}
			items[i].Listing, items[i].Detailed = enriched, true
			r.Update(i, items[i])
			read++
		}
		if err := progress(n + 1); err != nil {
			return gone, "", err
		}
		if (n+1)%saveEvery == 0 {
			markRemoved()
			if err := s.save(ctx, scan, items, r.Rate()); err != nil {
				return gone, "", err
			}
		}
	}
	return gone, "", nil
}

// pushedBack: the site answered with a status that says to slow down.
func pushedBack(err error) bool {
	var h interface{ HTTPStatus() int }
	if !errors.As(err, &h) {
		return false
	}
	code := h.HTTPStatus()
	return code == 429 || code == 403 || code >= 500
}

func (s *Scanner) sleep(ctx context.Context, d time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// jittered is d stretched by a random share of itself up to jitter.
func jittered(d time.Duration, jitter float64) time.Duration {
	return d + time.Duration(rand.Float64()*jitter*float64(d))
}

// save replaces the gems of the scan's category with results, in one
// transaction.
func (s *Scanner) save(ctx context.Context, scan Scan, items []Item, results []Rated) error {
	return pgx.BeginFunc(ctx, s.Service.pool, func(tx pgx.Tx) error {
		q := s.Service.q.WithTx(tx)
		if err := q.ClearGems(ctx, gemsdb.ClearGemsParams{Source: scan.Source, Category: scan.Category}); err != nil {
			return err
		}
		for _, x := range results {
			it := items[x.Item]
			if it.Listing.PriceValue == nil {
				continue // a category rates only priced ones; never store a row without
			}
			if err := q.InsertGem(ctx, row(scan, it, x)); err != nil {
				return fmt.Errorf("save %s: %w", it.Listing.ExternalID, err)
			}
		}
		return nil
	})
}

func row(scan Scan, it Item, x Rated) gemsdb.InsertGemParams {
	l := it.Listing
	fields, _ := json.Marshal(nonNil(x.Fields))
	lines, _ := json.Marshal(nonNil(x.Lines))
	components, _ := json.Marshal(nonNil(x.Components))
	attrs, _ := json.Marshal(x.Attrs)
	tr := x.Translations
	if tr == nil {
		tr = map[string]Text{}
	}
	translations, _ := json.Marshal(tr)
	score := x.Score
	if math.IsInf(score, -1) || math.IsNaN(score) {
		score = -1e9 // Postgres takes -Infinity, but JSON and sorting are simpler without
	}
	return gemsdb.InsertGemParams{
		Source: l.Source, Category: scan.Category, ExternalID: l.ExternalID, ScanID: scan.ID,
		Title: l.Title, Url: l.URL, ImageUrl: l.ImageURL, Price: l.Price, PriceValue: *l.PriceValue,
		Buy: x.Buy, Score: score, DisplayScore: int32(x.Display), PriceDiff: x.PriceDiff,
		Heading: x.Heading, Overall: x.Overall, Fields: fields, Components: components,
		Detailed: it.Detailed, Attrs: attrs, Lines: lines, Translations: translations,
	}
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
