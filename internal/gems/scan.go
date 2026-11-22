package gems

import (
	"cmp"
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

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/gemsdb"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Car is a stored ad with a make and a price, as the listings slice keeps it.
type Car struct {
	Listing  events.ListingDiscovered // ad-page details merged into Attrs
	Active   bool
	Detailed bool // its ad page was read
}

// Store is what a scan needs from the stored listings.
type Store interface {
	// StoreScanned keeps an ad without announcing it.
	StoreScanned(ctx context.Context, l events.ListingDiscovered) error
	// UnseenSince lists active ads not seen since since (Source, ExternalID,
	// Feed and Location only).
	UnseenSince(ctx context.Context, source string, since time.Time) ([]events.ListingDiscovered, error)
	MarkGoneIDs(ctx context.Context, source string, ids []string) (int64, error)
	StoredCars(ctx context.Context, source string) ([]Car, error)
	SaveDetails(ctx context.Context, before, enriched events.ListingDiscovered) error
}

// Scanner runs the scans admins request, one at a time. A scan has two
// passes: it reads every listing of the feed and rates each from what the
// list shows (saved at once, so the finder is usable), then reads the ad
// page of every potential buy not read before (deals.Candidate: fair price,
// no risky engine, a sane mileage) for its gearbox, body, region and
// equipment, and rates everything again.
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
// everything) every 100 ad pages, and on a site pushing back (429, 403, 5xx) a pause
// of 1, 2, 4 minutes; the third push without a good read between ends the
// pass, as do 10 failed reads without one.
const (
	breakEvery   = 100
	breakFor     = 20 * time.Second // to 40 s
	saveEvery    = 100
	pushbackWait = time.Minute
	maxPushbacks = 3
	maxFailures  = 10
)

// Run looks for requested scans until ctx is done.
func (s *Scanner) Run(ctx context.Context) {
	var names []string
	for _, n := range s.Sources.Names() {
		if _, ok := s.Sources[n].(sources.CarScan); ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
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
	slog.Info("gem scan started", "id", scan.ID, "source", scan.Source)
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
	src, err := s.Sources.Get(scan.Source)
	if err != nil {
		return out, err
	}
	cars, okCars := src.(sources.CarAds)
	whole, okScan := src.(sources.CarScan)
	if !okCars || !okScan {
		return out, fmt.Errorf("%s cannot be scanned for cars", scan.Source)
	}

	// Pass 1: every listing, rated from the list page.
	ads, complete, err := s.crawl(ctx, scan, src, whole.CarSaleFeed())
	if err != nil {
		return out, err
	}
	if !complete {
		out.notes = append(out.notes, fmt.Sprintf("stopped after %d pages before the end; sold cars not updated", s.MaxPages))
	} else if scan.StartedAt != nil {
		var note string
		out.gone, note, err = s.markGone(ctx, scan.Source, *scan.StartedAt, ads, cars)
		if err != nil {
			return out, err
		}
		out.complete = note == ""
		if note != "" {
			out.notes = append(out.notes, note)
		}
	}
	stored, err := s.Store.StoredCars(ctx, scan.Source)
	if err != nil {
		return out, fmt.Errorf("stored cars: %w", err)
	}
	r := newRater(stored, cars)
	results := r.rateAll()
	if err := s.save(ctx, scan, results); err != nil {
		return out, err
	}
	out.rated = len(results)

	// Pass 2: the potential buys' ad pages, then everything rated again
	// (peers' equipment changed too).
	gone, note, err := s.detail(ctx, scan, src, r, results)
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
	results = r.rateAll()
	if serr := s.save(sctx, scan, results); serr != nil {
		return out, serr
	}
	out.rated = len(results)
	return out, err
}

// errEmptyStart: the feed's first page listed nothing. A block or a layout
// change looks like that; it is never a real end.
var errEmptyStart = errors.New("the first page listed no cars; the site may be blocking us or have changed")

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

// maxGoneShare: if more of the stored cars than this went missing in one
// scan, the crawl more likely broke off (an empty page mid-feed) than half
// the market sold, so nothing is marked.
const maxGoneShare = 0.5

// markGone marks sold the cars for sale the scan did not see; note says so
// when it refused to.
func (s *Scanner) markGone(ctx context.Context, source string, since time.Time, ads int, cars sources.CarAds) (int, string, error) {
	unseen, err := s.Store.UnseenSince(ctx, source, since)
	if err != nil {
		return 0, "", fmt.Errorf("unseen: %w", err)
	}
	var ids []string
	for _, l := range unseen {
		if cars.RateableCar(l) {
			ids = append(ids, l.ExternalID)
		}
	}
	if len(ids) == 0 {
		return 0, "", nil
	}
	if share := float64(len(ids)) / float64(len(ids)+ads); share > maxGoneShare {
		return 0, fmt.Sprintf("%d of %d stored cars were missing, too many to be sold; sold cars not updated", len(ids), len(ids)+ads), nil
	}
	n, err := s.Store.MarkGoneIDs(ctx, source, ids)
	return int(n), "", err
}

// detail reads the ad page of every potential buy among results not read
// before, best first, paced by Wait plus DetailGap and backing off when the
// site pushes back. It saves the re-rated cars every saveEvery pages, so
// progress shows. A removed ad is marked sold. note says why it stopped
// early; err is only ctx's or a failed save.
func (s *Scanner) detail(ctx context.Context, scan Scan, src sources.Source, r *rater, results []result) (gone int, note string, err error) {
	enricher, ok := src.(sources.Enricher)
	if !ok {
		return 0, "", nil
	}
	var todo []int
	for _, x := range results {
		if !r.cars[x.car].stored.Detailed && deals.Candidate(x.ratings) {
			todo = append(todo, x.car)
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
		c := &r.cars[i]
		l := c.stored.Listing
		enriched, err := enricher.Enrich(ctx, l)
		switch {
		case ctx.Err() != nil:
			return gone, "", ctx.Err()
		case errors.Is(err, sources.ErrGone):
			pushbacks, failures = 0, 0
			removed = append(removed, l.ExternalID)
			c.stored.Active = false
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
			c.stored.Listing, c.stored.Detailed = enriched, true
			r.refresh(i)
			read++
		}
		if err := progress(n + 1); err != nil {
			return gone, "", err
		}
		if (n+1)%saveEvery == 0 {
			markRemoved()
			if err := s.save(ctx, scan, r.rateAll()); err != nil {
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

// save replaces the source's gems with results, in one transaction.
func (s *Scanner) save(ctx context.Context, scan Scan, results []result) error {
	return pgx.BeginFunc(ctx, s.Service.pool, func(tx pgx.Tx) error {
		q := s.Service.q.WithTx(tx)
		if err := q.ClearGems(ctx, scan.Source); err != nil {
			return err
		}
		for _, x := range results {
			if err := q.InsertGem(ctx, x.row(scan)); err != nil {
				return fmt.Errorf("save %s: %w", x.listing.ExternalID, err)
			}
		}
		return nil
	})
}

// rater rates every active car for sale against the stored ones like it.
type rater struct {
	cars    []ratedCar
	byModel map[string][]int // make|model, lower-cased -> indexes into cars
	terms   deals.Terms
	isCar   func(events.ListingDiscovered) bool
}

type ratedCar struct {
	stored Car
	car    deals.Car
	peer   deals.Peer
	ok     bool // a car for sale with a make, model and year
}

func newRater(stored []Car, cars sources.CarAds) *rater {
	r := &rater{byModel: map[string][]int{}, terms: cars.CarTerm, isCar: cars.RateableCar}
	for _, c := range stored {
		r.cars = append(r.cars, ratedCar{stored: c})
		i := len(r.cars) - 1
		r.refresh(i)
		if r.cars[i].ok {
			k := modelKey(r.cars[i].car)
			r.byModel[k] = append(r.byModel[k], i)
		}
	}
	return r
}

func modelKey(c deals.Car) string { return strings.ToLower(c.Make + "|" + c.Model) }

// refresh rereads car i from its listing (after its ad page was read).
func (r *rater) refresh(i int) {
	c := &r.cars[i]
	l := c.stored.Listing
	car, ok := deals.CarOf(l, r.terms)
	c.car, c.ok = car, ok && r.isCar(l) && l.PriceValue != nil
	if c.ok {
		c.peer = deals.Peer{Price: car.Price, Year: car.Year, MileageKm: car.MileageKm, Fuel: car.Fuel, Equipment: car.Equipment}
	}
}

type result struct {
	car      int // index into rater.cars
	info     deals.Car
	listing  events.ListingDiscovered
	detailed bool
	ratings  deals.Ratings
	score    float64
}

// rateAll rates every active car for sale that has enough to go on, best
// first. Sold ones still count as peers: their asking prices are the market.
func (r *rater) rateAll() []result {
	var out []result
	for i, c := range r.cars {
		if !c.ok || !c.stored.Active {
			continue
		}
		if x := r.rate(i); x.ratings.Price != nil || x.ratings.Engine != nil {
			if _, ok := x.ratings.Text(); ok {
				out = append(out, x)
			}
		}
	}
	sortResults(out)
	return out
}

func (r *rater) rate(i int) result {
	c := r.cars[i]
	var peers []deals.Peer
	for _, j := range r.byModel[modelKey(c.car)] {
		p := r.cars[j]
		if j == i || !p.ok || p.car.Year < c.car.Year-2 || p.car.Year > c.car.Year+2 {
			continue
		}
		if fp := c.stored.Listing.Fingerprint; fp != "" && p.stored.Listing.Fingerprint == fp {
			continue // its own earlier ad
		}
		peers = append(peers, p.peer)
	}
	ratings := deals.Rate(c.car, peers)
	return result{car: i, info: c.car, listing: c.stored.Listing, detailed: c.stored.Detailed, ratings: ratings, score: ratings.Score()}
}

func sortResults(rs []result) {
	slices.SortStableFunc(rs, func(a, b result) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.listing.ExternalID, b.listing.ExternalID))
	})
}

var buyNames = map[deals.Buy]string{deals.GoodBuy: "good", deals.AverageBuy: "average", deals.BadBuy: "bad", deals.CheckFirst: "check"}

func (x result) row(scan Scan) gemsdb.InsertGemParams {
	l := x.listing
	b, _, _ := x.ratings.Overall()
	overall, _ := x.ratings.Text()
	fields, _ := json.Marshal(deals.FieldsOf(x.ratings))
	components, _ := json.Marshal(x.ratings.Components())
	car := x.info
	score := x.score
	if math.IsInf(score, -1) {
		score = -1e9 // Postgres takes -Infinity, but JSON and sorting are simpler without
	}
	var diff *float64
	if x.ratings.Price != nil {
		diff = &x.ratings.Price.Diff
	}
	region, _ := l.Str("region")
	return gemsdb.InsertGemParams{
		Source: l.Source, ExternalID: l.ExternalID, ScanID: scan.ID, Title: l.Title, Url: l.URL, ImageUrl: l.ImageURL,
		Make: car.Make, Model: car.Model, Year: int32(car.Year), MileageKm: int32(car.MileageKm), Fuel: string(car.Fuel),
		Price: l.Price, PriceValue: *l.PriceValue, Buy: buyNames[b], Score: score, Overall: overall, Fields: fields,
		Gearbox: string(car.Gearbox), Inspection: inspectionOf(x.ratings), Body: string(car.Body), Region: region, EngineL: car.EngineL, Detailed: x.detailed,
		DisplayScore: int32(deals.Display(x.score)), PriceDiff: diff, Components: components,
	}
}

// inspectionOf is what the filter goes by: valid, none (also one that ran
// out), or "" when unknown.
func inspectionOf(r deals.Ratings) string {
	switch i := r.Inspection; {
	case i == nil:
		return ""
	case i.Valid:
		return string(deals.Inspected)
	}
	return string(deals.NotInspected)
}
