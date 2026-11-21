package gems

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Needs a real Postgres: TEST_DATABASE_URL=postgres://... go test ./internal/gems
func newTestService(t *testing.T) *Service {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS gems, gem_scans, gems_migrations`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool)
	for range 2 { // the second run proves migrations are idempotent
		if err := svc.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

// fakeSite lists cars for sale on "cars/sell", one page per element of
// pages; past the end it repeats the last page, as sites do.
type fakeSite struct {
	pages [][]events.ListingDiscovered
	reads []string // ad pages read
}

func (*fakeSite) Name() string { return "ss.com" }
func (f *fakeSite) FetchFeed(_ context.Context, _ string, page int) ([]events.ListingDiscovered, error) {
	return slices.Clone(f.pages[min(page, len(f.pages))-1]), nil
}
func (*fakeSite) PlanFeeds(w []string, _ int) []string            { return w }
func (*fakeSite) InScope([]string, events.ListingDiscovered) bool { return true }
func (*fakeSite) CarSaleFeed() string                             { return "cars/sell" }
func (*fakeSite) CarTerm(key, v string) string                    { return map[bool]string{true: v}[deals.Knows(key, v)] }
func (*fakeSite) RateableCar(l events.ListingDiscovered) bool {
	return strings.HasSuffix(l.Feed, "/sell")
}
func (*fakeSite) EnrichableKeys() []string { return []string{"equipment"} }
func (f *fakeSite) Enrich(_ context.Context, l events.ListingDiscovered) (events.ListingDiscovered, error) {
	f.reads = append(f.reads, l.ExternalID)
	out := l
	out.Attrs = map[string]any{"equipment": []any{"climate control", "adaptive cruise control", "reversing camera",
		"xenon lights", "keyless entry", "front parking sensors", "360° cameras", "sunroof", "lane assist", "sport package"}}
	for k, v := range l.Attrs {
		out.Attrs[k] = v
	}
	return out, nil
}

// fakeStore keeps stored ads in memory.
type fakeStore struct {
	cars    map[string]*Car
	scanned map[string]bool
}

func (s *fakeStore) StoreScanned(_ context.Context, l events.ListingDiscovered) error {
	if c, ok := s.cars[l.ExternalID]; ok {
		c.Listing, c.Active = l, true
	} else {
		s.cars[l.ExternalID] = &Car{Listing: l, Active: true}
	}
	s.scanned[l.ExternalID] = true
	return nil
}
func (s *fakeStore) UnseenSince(context.Context, string, time.Time) ([]events.ListingDiscovered, error) {
	var out []events.ListingDiscovered
	for id, c := range s.cars {
		if c.Active && !s.scanned[id] {
			out = append(out, events.ListingDiscovered{ExternalID: id, Feed: c.Listing.Feed, Location: c.Listing.Location})
		}
	}
	return out, nil
}
func (s *fakeStore) MarkGoneIDs(_ context.Context, _ string, ids []string) (int64, error) {
	for _, id := range ids {
		s.cars[id].Active = false
	}
	return int64(len(ids)), nil
}
func (s *fakeStore) StoredCars(context.Context, string) ([]Car, error) {
	var out []Car
	for _, c := range s.cars {
		out = append(out, *c)
	}
	return out, nil
}
func (s *fakeStore) SaveDetails(_ context.Context, _, enriched events.ListingDiscovered) error {
	s.cars[enriched.ExternalID].Listing, s.cars[enriched.ExternalID].Detailed = enriched, true
	return nil
}

func bmw(id string, price float64) events.ListingDiscovered {
	return events.ListingDiscovered{
		Source: "ss.com", ExternalID: id, Title: "BMW 520d", URL: "https://x/" + id, Location: "cars/bmw/520",
		Price: "€", PriceValue: &price,
		Attrs: map[string]any{"make": "BMW", "model": "520", "year": 2016.0, "engine_l": 2.0, "fuel": "diesel", "mileage_km": 180000.0},
	}
}

func TestScan(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	cheap := bmw("cheap", 7000) // 30% below its peers: a steal
	lada := events.ListingDiscovered{Source: "ss.com", ExternalID: "lada", Title: "Niva", Location: "cars/lada/niva",
		Price: "€", PriceValue: new(float64), Attrs: map[string]any{"make": "Lada", "model": "Niva", "year": 2010.0}}
	*lada.PriceValue = 2000
	site := &fakeSite{pages: [][]events.ListingDiscovered{
		{cheap, bmw("p1", 10000), bmw("p2", 10200), bmw("p3", 9800)},
		{bmw("p4", 10000), bmw("p5", 10100), lada},
	}}
	sold := bmw("sold", 9900)
	sold.Feed = "cars/sell"
	store := &fakeStore{cars: map[string]*Car{"sold": {Listing: sold, Active: true}}, scanned: map[string]bool{}}
	scanner := &Scanner{
		Service: svc, Store: store, Sources: sources.NewRegistry(site),
		Wait:   func(ctx context.Context, _ string) bool { return ctx.Err() == nil },
		Detail: 1, MaxPages: 10, PollEvery: time.Millisecond,
	}

	if _, err := svc.RequestScan(ctx, "ss.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestScan(ctx, "ss.com"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request: err = %v, want ErrBusy", err)
	}
	r, err := svc.q.ClaimScan(ctx, []string{"ss.com"})
	if err != nil {
		t.Fatal(err)
	}
	scanner.run(ctx, scanOf(r))

	scan, ok, err := svc.LatestScan(ctx, "ss.com")
	if err != nil || !ok {
		t.Fatalf("latest: %v %v", ok, err)
	}
	if scan.Status != "done" || scan.Pages != 3 || scan.Ads != 7 || scan.Gone != 1 || scan.Rated != 6 {
		t.Errorf("scan = %+v, want done after 3 pages (the last a repeat), 7 ads, 1 gone, 6 rated (not the Niva)", scan)
	}
	if store.cars["sold"].Active {
		t.Error("the ad no longer listed is still active")
	}
	if !slices.Equal(site.reads, []string{"cheap"}) {
		t.Errorf("ad pages read = %v, want just the best one", site.reads)
	}

	page, err := svc.List(ctx, "ss.com", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 6 || len(page.Items) != 6 {
		t.Fatalf("listed %d of %d, want the 6 BMWs (the Niva has nothing to judge by)", len(page.Items), page.Total)
	}
	best := page.Items[0]
	if best.ExternalID != "cheap" || best.Buy != "good" || best.Fuel != "diesel" {
		t.Errorf("best = %+v, want the cheap BMW, a good buy", best)
	}
	var names []string
	for _, f := range best.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, "🧰 Equipment verdict") {
		t.Errorf("best's verdicts %v lack the equipment its ad page added", names)
	}
	if len(page.Models) != 1 || page.Models[0].Model != "520" || page.Models[0].Count != 6 {
		t.Errorf("models = %+v", page.Models)
	}

	if p, _ := svc.List(ctx, "ss.com", Query{Buy: "good"}); p.Total != 1 {
		t.Errorf("good buys = %d, want 1", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{MaxPrice: 9000}); p.Total != 1 {
		t.Errorf("under 9000 = %d, want 1", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{Sort: "price"}); len(p.Items) == 0 || p.Items[0].PriceValue != 7000 {
		t.Errorf("cheapest first: %+v", p.Items)
	}
	if _, err := svc.List(ctx, "ss.com", Query{Buy: "maybe"}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("bad filter: err = %v, want ErrBadQuery", err)
	}

	// Done, a new scan may be requested.
	if _, err := svc.RequestScan(ctx, "ss.com"); err != nil {
		t.Errorf("request after a finished scan: %v", err)
	}
}

// runScan requests, claims and runs one scan, returning its record.
func runScan(t *testing.T, svc *Service, scanner *Scanner) Scan {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.RequestScan(ctx, "ss.com"); err != nil {
		t.Fatal(err)
	}
	r, err := svc.q.ClaimScan(ctx, []string{"ss.com"})
	if err != nil {
		t.Fatal(err)
	}
	scanner.run(ctx, scanOf(r))
	scan, _, err := svc.LatestScan(ctx, "ss.com")
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

// A scan that cannot have seen the whole feed marks nothing sold.
func TestScanIncomplete(t *testing.T) {
	svc := newTestService(t)
	newStore := func(n int) *fakeStore {
		s := &fakeStore{cars: map[string]*Car{}, scanned: map[string]bool{}}
		for i := range n {
			l := bmw("old"+string(rune('a'+i)), 10000)
			l.Feed = "cars/sell"
			s.cars[l.ExternalID] = &Car{Listing: l, Active: true}
		}
		return s
	}
	scanner := func(site *fakeSite, store *fakeStore, maxPages int) *Scanner {
		return &Scanner{
			Service: svc, Store: store, Sources: sources.NewRegistry(site),
			Wait:   func(ctx context.Context, _ string) bool { return ctx.Err() == nil },
			Detail: 0, MaxPages: maxPages, PollEvery: time.Millisecond,
		}
	}
	active := func(s *fakeStore) (n int) {
		for _, c := range s.cars {
			if c.Active {
				n++
			}
		}
		return n
	}

	// An empty first page is a block or a redesign, never the end.
	store := newStore(3)
	scan := runScan(t, svc, scanner(&fakeSite{pages: [][]events.ListingDiscovered{{}}}, store, 10))
	if scan.Status != "failed" || !strings.Contains(scan.Error, "first page") || active(store) != 3 {
		t.Errorf("empty first page: %+v, %d active; want failed, nothing sold", scan, active(store))
	}

	// Capped before the end: rated, but sold cars untouched, and said so.
	store = newStore(3)
	site := &fakeSite{pages: [][]events.ListingDiscovered{{bmw("a", 9000)}, {bmw("b", 9100)}}}
	scan = runScan(t, svc, scanner(site, store, 1))
	if scan.Status != "done" || scan.Complete || !strings.Contains(scan.Note, "stopped after 1 pages") || active(store) != 4 {
		t.Errorf("capped: %+v, %d active; want done, incomplete, nothing sold", scan, active(store))
	}

	// An empty page mid-feed ends the crawl early: most stored cars look
	// gone, which is implausible, so none is marked.
	store = newStore(5)
	site = &fakeSite{pages: [][]events.ListingDiscovered{{bmw("a", 9000)}, {}}}
	scan = runScan(t, svc, scanner(site, store, 10))
	if scan.Complete || !strings.Contains(scan.Note, "too many") || scan.Gone != 0 || active(store) != 6 {
		t.Errorf("empty mid-feed: %+v, %d active; want nothing sold", scan, active(store))
	}

	// A plausible share gone is marked, and the scan is complete.
	store = newStore(1)
	site = &fakeSite{pages: [][]events.ListingDiscovered{{bmw("a", 9000), bmw("b", 9100), bmw("c", 9200)}}}
	scan = runScan(t, svc, scanner(site, store, 10))
	if !scan.Complete || scan.Note != "" || scan.Gone != 1 {
		t.Errorf("normal: %+v, want complete with 1 sold", scan)
	}
}
