package gems

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
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
	pages     [][]events.ListingDiscovered
	reads     []string        // ad pages read
	enrichErr error           // every ad page fails with it
	removed   map[string]bool // ads whose page says they were removed
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
	if f.enrichErr != nil {
		return l, f.enrichErr
	}
	if f.removed[l.ExternalID] {
		return l, sources.ErrGone
	}
	out := l
	out.Attrs = map[string]any{"body": "wagon", "region": "Rīga", "gearbox": "automatic", "inspection": "valid",
		"inspection_until": "2030-05", "equipment": []any{"climate control", "adaptive cruise control", "reversing camera",
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

// pacing records a scanner's waits and pauses without spending the time.
type pacing struct {
	waits  int
	pauses []time.Duration
}

func (p *pacing) wait(ctx context.Context, _ string) bool { p.waits++; return ctx.Err() == nil }
func (p *pacing) sleep(ctx context.Context, d time.Duration) bool {
	p.pauses = append(p.pauses, d)
	return ctx.Err() == nil
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
		{bmw("p4", 10000), bmw("p5", 10100), bmw("dear", 12800), lada},
	}}
	sold := bmw("sold", 9900)
	sold.Feed = "cars/sell"
	store := &fakeStore{cars: map[string]*Car{"sold": {Listing: sold, Active: true}}, scanned: map[string]bool{}}
	pace := &pacing{}
	scanner := &Scanner{
		Service: svc, Store: store, Sources: sources.NewRegistry(site),
		Wait: pace.wait, Sleep: pace.sleep, DetailGap: time.Second, MaxPages: 10, PollEvery: time.Millisecond,
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
	if scan.Status != "done" || scan.Pages != 3 || scan.Ads != 8 || scan.Gone != 1 || scan.Rated != 7 {
		t.Errorf("scan = %+v, want done after 3 pages (the last a repeat), 8 ads, 1 gone, 7 rated (not the Niva)", scan)
	}
	if store.cars["sold"].Active {
		t.Error("the ad no longer listed is still active")
	}
	// Pass 2: every potential buy, best first; not the overpriced one, nor
	// the Niva with nothing to compare its price with.
	if len(site.reads) != 6 || site.reads[0] != "cheap" || slices.Contains(site.reads, "dear") || slices.Contains(site.reads, "lada") {
		t.Errorf("ad pages read = %v, want the 6 fairly priced BMWs, cheap first", site.reads)
	}
	if scan.Phase != "detailing" || scan.DetailTotal != 6 || scan.Detailed != 6 {
		t.Errorf("detail progress = %s %d/%d, want detailing 6/6", scan.Phase, scan.Detailed, scan.DetailTotal)
	}
	// Each ad page waited for the site's throttle and its own jittered gap.
	if pace.waits != 3+6 {
		t.Errorf("%d throttle waits, want one per feed page (3) and ad page (6)", pace.waits)
	}
	for _, d := range pace.pauses {
		if d < time.Second || d > 3*time.Second/2 {
			t.Errorf("pause %v, want the 1 s gap stretched by at most half", d)
		}
	}

	page, err := svc.List(ctx, "ss.com", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 7 || len(page.Items) != 7 {
		t.Fatalf("listed %d of %d, want the 7 BMWs (the Niva has nothing to judge by)", len(page.Items), page.Total)
	}
	best := page.Items[0]
	if best.ExternalID != "cheap" || best.Buy != "good" || best.Fuel != "diesel" || best.Body != "wagon" ||
		best.Region != "Rīga" || best.Gearbox != "automatic" || !best.Detailed || best.Score <= 50 {
		t.Errorf("best = %+v, want the cheap BMW, a good buy with its ad page's details", best)
	}
	if best.PriceDiff == nil || *best.PriceDiff > -0.25 || len(best.Components) < 3 {
		t.Errorf("best: diff %v, components %+v", best.PriceDiff, best.Components)
	}
	if page.Detailed != 6 || page.All != 7 {
		t.Errorf("detailed %d of %d, want 6 of 7", page.Detailed, page.All)
	}
	var names []string
	for _, f := range best.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, "🧰 Equipment verdict") {
		t.Errorf("best's verdicts %v lack the equipment its ad page added", names)
	}
	f := page.Facets
	if len(f.Models) != 1 || f.Models[0].Model != "520" || f.Models[0].Count != 7 {
		t.Errorf("models = %+v", f.Models)
	}
	if len(f.Bodies) != 1 || f.Bodies[0] != (Count{"wagon", 6}) || len(f.Regions) != 1 || f.Regions[0] != (Count{"Rīga", 6}) {
		t.Errorf("bodies %+v, regions %+v; want the 6 read ones", f.Bodies, f.Regions)
	}

	if p, _ := svc.List(ctx, "ss.com", Query{Buy: "good"}); p.Total != 1 {
		t.Errorf("good buys = %d, want 1", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{Bodies: []string{"wagon", "sedan"}, Regions: []string{"Rīga"}}); p.Total != 6 {
		t.Errorf("wagons or sedans in Rīga = %d, want 6", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{InspectedOnly: true}); p.Total != 6 {
		t.Errorf("with a valid inspection = %d, want the 6 read", p.Total)
	}
	if best.Inspection != "valid" {
		t.Errorf("best's inspection = %q", best.Inspection)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{Bodies: []string{"sedan"}}); p.Total != 0 {
		t.Errorf("sedans = %d, want 0", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{MinPrice: 10000, MaxYear: 2016, Fuels: []string{"diesel"}}); p.Total != 5 {
		t.Errorf("diesels from 10000 = %d, want 5", p.Total)
	}
	if p, _ := svc.List(ctx, "ss.com", Query{Sort: "discount"}); len(p.Items) == 0 || p.Items[0].ExternalID != "cheap" {
		t.Errorf("furthest below similar ads first: %+v", p.Items)
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

	// Done, a new scan may be requested; ad pages read before are not read
	// again.
	site.reads = nil
	if scan := runScan(t, svc, scanner); scan.Status != "done" || scan.DetailTotal != 0 || len(site.reads) != 0 {
		t.Errorf("second scan: %+v, read %v; want nothing new to read", scan, site.reads)
	}
}

// statusErr is a site's HTTP error, as sources report one.
type statusErr int

func (e statusErr) Error() string   { return "status " + strconv.Itoa(int(e)) }
func (e statusErr) HTTPStatus() int { return int(e) }

// A site pushing back on ad pages is given longer pauses, then left alone.
func TestScanBacksOff(t *testing.T) {
	svc := newTestService(t)
	var ads []events.ListingDiscovered
	for i := range 8 {
		ads = append(ads, bmw("c"+strconv.Itoa(i), 10000+float64(i)*50))
	}
	site := &fakeSite{pages: [][]events.ListingDiscovered{ads}, enrichErr: statusErr(429)}
	pace := &pacing{}
	scanner := &Scanner{
		Service: svc, Store: &fakeStore{cars: map[string]*Car{}, scanned: map[string]bool{}}, Sources: sources.NewRegistry(site),
		Wait: pace.wait, Sleep: pace.sleep, MaxPages: 10, PollEvery: time.Millisecond,
	}
	scan := runScan(t, svc, scanner)
	if scan.Status != "done" || !strings.Contains(scan.Note, "pushed back") || !strings.Contains(scan.Note, "read 0 of 8") {
		t.Errorf("scan = %+v, want done, noting the push back", scan)
	}
	if len(site.reads) != maxPushbacks {
		t.Errorf("%d ad pages tried, want %d", len(site.reads), maxPushbacks)
	}
	var backoffs []time.Duration
	for _, d := range pace.pauses {
		if d >= time.Minute {
			backoffs = append(backoffs, d)
		}
	}
	if !slices.Equal(backoffs, []time.Duration{time.Minute, 2 * time.Minute}) {
		t.Errorf("backoffs = %v, want 1m, 2m before giving up", backoffs)
	}
	if scan.Rated != 8 {
		t.Errorf("rated %d, want all 8 from their list page", scan.Rated)
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
			Wait:     func(ctx context.Context, _ string) bool { return ctx.Err() == nil },
			Sleep:    func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
			MaxPages: maxPages, PollEvery: time.Millisecond,
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

// An ad whose page says it was removed is marked sold and leaves the list.
func TestScanDropsRemoved(t *testing.T) {
	svc := newTestService(t)
	var ads []events.ListingDiscovered
	for i := range 6 {
		ads = append(ads, bmw("c"+strconv.Itoa(i), 10000+float64(i)*50))
	}
	site := &fakeSite{pages: [][]events.ListingDiscovered{ads}, removed: map[string]bool{"c2": true}}
	store := &fakeStore{cars: map[string]*Car{}, scanned: map[string]bool{}}
	pace := &pacing{}
	scan := runScan(t, svc, &Scanner{
		Service: svc, Store: store, Sources: sources.NewRegistry(site),
		Wait: pace.wait, Sleep: pace.sleep, MaxPages: 10, PollEvery: time.Millisecond,
	})
	if scan.Status != "done" || scan.Gone != 1 || scan.Rated != 5 || store.cars["c2"].Active {
		t.Errorf("scan = %+v, c2 active %v; want 1 gone, 5 rated", scan, store.cars["c2"].Active)
	}
	page, err := svc.List(context.Background(), "ss.com", Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range page.Items {
		if g.ExternalID == "c2" {
			t.Error("the removed ad is still listed")
		}
	}
}
