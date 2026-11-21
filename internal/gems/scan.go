package gems

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

// Scanner runs the scans admins request, one at a time.
type Scanner struct {
	Service *Service
	Store   Store
	Sources sources.Registry
	// Wait spaces a source's feed requests; shared with its regular
	// scraping, so a scan does not double the load on the site.
	Wait func(ctx context.Context, source string) bool
	// Detail is how many of the best-rated cars get their ad page read
	// (equipment, gearbox) and are rated again.
	Detail int
	// MaxPages caps a scan; one that reaches it marks nothing sold.
	MaxPages  int
	PollEvery time.Duration
}

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
	note        string // why it was not complete, or what it skipped
}

func (s *Scanner) run(ctx context.Context, scan Scan) {
	slog.Info("gem scan started", "id", scan.ID, "source", scan.Source)
	out, err := s.scan(ctx, scan)
	status, msg := "done", ""
	if err != nil {
		status, msg = "failed", err.Error()
		slog.Error("gem scan failed", "id", scan.ID, "err", err)
	} else {
		slog.Info("gem scan done", "id", scan.ID, "gone", out.gone, "rated", out.rated, "complete", out.complete, "note", out.note)
	}
	// A fresh context: a scan cut short by shutdown still records it.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Service.q.FinishScan(fctx, gemsdb.FinishScanParams{
		ID: scan.ID, Status: status, Gone: int32(out.gone), Rated: int32(out.rated), Error: msg,
		Complete: out.complete, Note: out.note,
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

	ads, complete, err := s.crawl(ctx, scan, src, whole.CarSaleFeed())
	if err != nil {
		return out, err
	}
	if !complete {
		out.note = fmt.Sprintf("stopped after %d pages before the end; sold cars not updated", s.MaxPages)
	} else if scan.StartedAt != nil {
		out.gone, out.note, err = s.markGone(ctx, scan.Source, *scan.StartedAt, ads, cars)
		if err != nil {
			return out, err
		}
		out.complete = out.note == ""
	}

	stored, err := s.Store.StoredCars(ctx, scan.Source)
	if err != nil {
		return out, fmt.Errorf("stored cars: %w", err)
	}
	r := newRater(stored, cars)
	results := r.rateAll()
	results = s.detailTop(ctx, src, r, results)
	if err := s.save(ctx, scan, results); err != nil {
		return out, err
	}
	out.rated = len(results)
	return out, nil
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

// detailTop reads the ad pages of the best-rated cars not read before, for
// their equipment and gearbox, and rates them again. A removed one is
// marked sold and dropped.
func (s *Scanner) detailTop(ctx context.Context, src sources.Source, r *rater, results []result) []result {
	enricher, ok := src.(sources.Enricher)
	if !ok || s.Detail <= 0 {
		return results
	}
	var drop []string
	read := 0
	for i := range results {
		if read == s.Detail || ctx.Err() != nil {
			break
		}
		c := &r.cars[results[i].car]
		if c.stored.Detailed || math.IsInf(results[i].score, -1) {
			continue
		}
		read++
		l := c.stored.Listing
		enriched, err := enricher.Enrich(ctx, l)
		switch {
		case errors.Is(err, sources.ErrGone):
			drop = append(drop, l.ExternalID)
			continue
		case err != nil:
			slog.Warn("gem scan: ad page unavailable", "id", l.ExternalID, "err", err)
			continue
		}
		if err := s.Store.SaveDetails(ctx, l, enriched); err != nil {
			slog.Warn("gem scan: save ad page", "id", l.ExternalID, "err", err)
		}
		c.stored.Listing, c.stored.Detailed = enriched, true
		r.refresh(results[i].car)
		results[i] = r.rate(results[i].car)
	}
	if len(drop) > 0 {
		if _, err := s.Store.MarkGoneIDs(ctx, src.Name(), drop); err != nil {
			slog.Warn("gem scan: mark removed", "err", err)
		}
		results = slices.DeleteFunc(results, func(x result) bool { return slices.Contains(drop, r.cars[x.car].stored.Listing.ExternalID) })
	}
	sortResults(results)
	return results
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
	car     int // index into rater.cars
	info    deals.Car
	listing events.ListingDiscovered
	ratings deals.Ratings
	score   float64
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
	return result{car: i, info: c.car, listing: c.stored.Listing, ratings: ratings, score: ratings.Score()}
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
	car := x.info
	score := x.score
	if math.IsInf(score, -1) {
		score = -1e9 // Postgres takes -Infinity, but JSON and sorting are simpler without
	}
	return gemsdb.InsertGemParams{
		Source: l.Source, ExternalID: l.ExternalID, ScanID: scan.ID, Title: l.Title, Url: l.URL, ImageUrl: l.ImageURL,
		Make: car.Make, Model: car.Model, Year: int32(car.Year), MileageKm: int32(car.MileageKm), Fuel: string(car.Fuel),
		Price: l.Price, PriceValue: *l.PriceValue, Buy: buyNames[b], Score: score, Overall: overall, Fields: fields,
	}
}
