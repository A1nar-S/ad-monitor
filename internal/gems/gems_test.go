package gems

import (
	"cmp"
	"context"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
	svc := NewService(pool, things{})
	for range 2 { // the second run proves migrations are idempotent
		if err := svc.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

// things is a category to test the engine with: an item is rated by its
// price alone (cheaper is better), and one under 10 000 is worth its ad
// page, which adds a shape.
type things struct{}

func (things) ID() string { return "things" }
func (things) Spec() Spec {
	return Spec{
		Fields: []Field{
			{Key: "colour", Kind: Choice, Control: "chips", Card: true},
			{Key: "shape", Kind: Choice, Control: "chips", Detail: true},
			{Key: "year", Kind: Range, Control: "range"},
		},
		Sorts: []Sort{{ID: "newest", Key: "year", Desc: true}},
	}
}
func (things) Belongs(_ sources.Source, l events.ListingDiscovered) bool {
	return strings.HasSuffix(l.Feed, "/sell")
}
func (things) Rater(_ sources.Source, items []Item) (Rater, bool) {
	return &thingRater{items: items}, true
}

type thingRater struct{ items []Item }

func (r *thingRater) Update(i int, it Item) { r.items[i] = it }
func (r *thingRater) Rate() []Rated {
	var out []Rated
	for i, it := range r.items {
		l := it.Listing
		if !it.Active || l.PriceValue == nil || !strings.HasSuffix(l.Feed, "/sell") {
			continue
		}
		p := *l.PriceValue
		x := Rated{Item: i, Score: -p / 1000, Display: int(100 - p/200), Buy: "average", Heading: "Thing " + l.ExternalID,
			Overall: "fine", Candidate: p < 10000, Attrs: map[string]any{}}
		if p < 8000 {
			x.Buy = "good"
		}
		if p > 50000 {
			x.Score = math.Inf(-1)
		}
		d := p/10000 - 1
		x.PriceDiff = &d
		x.Components = []Component{{Key: "price", X: -d, Weight: 3, Points: -3 * d}}
		for _, k := range []string{"colour", "shape"} {
			if v, ok := l.Str(k); ok {
				x.Attrs[k] = v
			}
		}
		if y, ok := l.Num("year"); ok {
			x.Attrs["year"] = y
		}
		out = append(out, x)
	}
	slices.SortStableFunc(out, func(a, b Rated) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(r.items[a.Item].Listing.ExternalID, r.items[b.Item].Listing.ExternalID))
	})
	return out
}

// fakeSite lists things for sale on "things/sell", one page per element of
// pages; past the end it repeats the last page, as sites do.
type fakeSite struct {
	pages     [][]events.ListingDiscovered
	reads     []string        // ad pages read
	enrichErr error           // every ad page fails with it
	removed   map[string]bool // ads whose page says they were removed
}

func (*fakeSite) Name() string { return "site" }
func (f *fakeSite) FetchFeed(_ context.Context, _ string, page int) ([]events.ListingDiscovered, error) {
	return slices.Clone(f.pages[min(page, len(f.pages))-1]), nil
}
func (*fakeSite) PlanFeeds(w []string, _ int) []string            { return w }
func (*fakeSite) InScope([]string, events.ListingDiscovered) bool { return true }
func (*fakeSite) ScanScope(category string) (sources.ScanScope, bool) {
	return sources.ScanScope{Feed: "things/sell", Location: "things/"}, category == "things"
}
func (*fakeSite) EnrichableKeys() []string { return []string{"shape"} }
func (f *fakeSite) Enrich(_ context.Context, l events.ListingDiscovered) (events.ListingDiscovered, error) {
	f.reads = append(f.reads, l.ExternalID)
	if f.enrichErr != nil {
		return l, f.enrichErr
	}
	if f.removed[l.ExternalID] {
		return l, sources.ErrGone
	}
	out := l
	out.Attrs = map[string]any{"shape": "round"}
	for k, v := range l.Attrs {
		out.Attrs[k] = v
	}
	return out, nil
}

// fakeStore keeps stored ads in memory.
type fakeStore struct {
	ads     map[string]*Item
	scanned map[string]bool
}

func newStore() *fakeStore { return &fakeStore{ads: map[string]*Item{}, scanned: map[string]bool{}} }

func (s *fakeStore) StoreScanned(_ context.Context, l events.ListingDiscovered) error {
	if c, ok := s.ads[l.ExternalID]; ok {
		c.Listing, c.Active = l, true
	} else {
		s.ads[l.ExternalID] = &Item{Listing: l, Active: true}
	}
	s.scanned[l.ExternalID] = true
	return nil
}
func (s *fakeStore) UnseenSince(context.Context, string, time.Time) ([]events.ListingDiscovered, error) {
	var out []events.ListingDiscovered
	for id, c := range s.ads {
		if c.Active && !s.scanned[id] {
			out = append(out, events.ListingDiscovered{ExternalID: id, Feed: c.Listing.Feed, Location: c.Listing.Location})
		}
	}
	return out, nil
}
func (s *fakeStore) MarkGoneIDs(_ context.Context, _ string, ids []string) (int64, error) {
	for _, id := range ids {
		s.ads[id].Active = false
	}
	return int64(len(ids)), nil
}
func (s *fakeStore) Stored(_ context.Context, _, location string) ([]Item, error) {
	var out []Item
	for _, c := range s.ads {
		if strings.HasPrefix(c.Listing.Location, location) {
			out = append(out, *c)
		}
	}
	return out, nil
}
func (s *fakeStore) SaveDetails(_ context.Context, _, enriched events.ListingDiscovered) error {
	s.ads[enriched.ExternalID].Listing, s.ads[enriched.ExternalID].Detailed = enriched, true
	return nil
}

func thing(id string, price float64, colour string, year float64) events.ListingDiscovered {
	return events.ListingDiscovered{
		Source: "site", ExternalID: id, Title: "A thing", URL: "https://x/" + id, Location: "things/" + colour,
		Price: "€", PriceValue: &price, Attrs: map[string]any{"colour": colour, "year": year},
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

func newScanner(svc *Service, site *fakeSite, store *fakeStore, pace *pacing, maxPages int) *Scanner {
	return &Scanner{
		Service: svc, Store: store, Sources: sources.NewRegistry(site),
		Wait: pace.wait, Sleep: pace.sleep, DetailGap: time.Second, MaxPages: maxPages, PollEvery: time.Millisecond,
	}
}

// runScan requests, claims and runs one scan, returning its record.
func runScan(t *testing.T, svc *Service, scanner *Scanner) Scan {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.RequestScan(ctx, "site", "things"); err != nil {
		t.Fatal(err)
	}
	r, err := svc.q.ClaimScan(ctx, []string{"site"})
	if err != nil {
		t.Fatal(err)
	}
	scanner.run(ctx, scanOf(r))
	scan, _, err := svc.LatestScan(ctx, "site", "things")
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

func TestScan(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	site := &fakeSite{pages: [][]events.ListingDiscovered{
		{thing("a", 5000, "red", 2020), thing("b", 9000, "blue", 2015), thing("c", 12000, "red", 2018)},
		{thing("d", 7000, "blue", 2022), thing("huge", 90000, "red", 2010)},
	}}
	store := newStore()
	sold := thing("sold", 6000, "red", 2019)
	sold.Feed = "things/sell"
	store.ads["sold"] = &Item{Listing: sold, Active: true}
	pace := &pacing{}
	scanner := newScanner(svc, site, store, pace, 10)

	if _, err := svc.RequestScan(ctx, "site", "things"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestScan(ctx, "site", "things"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request: err = %v, want ErrBusy", err)
	}
	if _, err := svc.RequestScan(ctx, "site", "flats"); !errors.Is(err, ErrBadQuery) {
		t.Errorf("unknown category: err = %v, want ErrBadQuery", err)
	}
	r, err := svc.q.ClaimScan(ctx, []string{"site"})
	if err != nil {
		t.Fatal(err)
	}
	scanner.run(ctx, scanOf(r))

	scan, ok, err := svc.LatestScan(ctx, "site", "things")
	if err != nil || !ok {
		t.Fatalf("latest: %v %v", ok, err)
	}
	if scan.Status != "done" || scan.Category != "things" || scan.Pages != 3 || scan.Ads != 5 || scan.Gone != 1 || scan.Rated != 5 {
		t.Errorf("scan = %+v, want done after 3 pages (the last a repeat), 5 ads, 1 gone, 5 rated", scan)
	}
	if store.ads["sold"].Active {
		t.Error("the ad no longer listed is still active")
	}
	// Pass 2: the candidates' ad pages, best first.
	if !slices.Equal(site.reads, []string{"a", "d", "b"}) {
		t.Errorf("ad pages read = %v, want a, d, b (under 10 000, best first)", site.reads)
	}
	if scan.Phase != "detailing" || scan.DetailTotal != 3 || scan.Detailed != 3 {
		t.Errorf("detail progress = %s %d/%d, want detailing 3/3", scan.Phase, scan.Detailed, scan.DetailTotal)
	}
	if pace.waits != 3+3 {
		t.Errorf("%d throttle waits, want one per feed page (3) and ad page (3)", pace.waits)
	}
	for _, d := range pace.pauses {
		if d < time.Second || d > 3*time.Second/2 {
			t.Errorf("pause %v, want the 1 s gap stretched by at most half", d)
		}
	}

	page, err := svc.List(ctx, "site", "things", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Items) != 5 || page.Detailed != 3 || page.All != 5 {
		t.Fatalf("listed %d of %d, %d of %d detailed; want 5, 3 detailed", len(page.Items), page.Total, page.Detailed, page.All)
	}
	best, last := page.Items[0], page.Items[4]
	if best.ExternalID != "a" || best.Heading != "Thing a" || best.Buy != "good" || best.Score != 75 || !best.Detailed ||
		best.Attrs["shape"] != "round" || best.Attrs["year"] != 2020.0 || best.PriceDiff == nil || len(best.Components) != 1 {
		t.Errorf("best = %+v", best)
	}
	if last.ExternalID != "huge" {
		t.Errorf("last = %s, want the one with too little known", last.ExternalID)
	}
	f := page.Facets
	if len(f["colour"]) != 2 || f["colour"][0] != (Facet{Value: "blue", Count: 2}) || len(f["shape"]) != 1 || f["shape"][0].Count != 3 {
		t.Errorf("facets = %+v", f)
	}

	for _, tt := range []struct {
		name string
		q    Query
		want []string
	}{
		{"good buys", Query{Buy: "good"}, []string{"a", "d"}},
		{"red", Query{Choices: map[string][]string{"colour": {"red"}}}, []string{"a", "c", "huge"}},
		{"red or blue, round", Query{Choices: map[string][]string{"colour": {"red", "blue"}, "shape": {"round"}}}, []string{"a", "d", "b"}},
		{"from 2018", Query{Ranges: map[string][2]float64{"year": {2018, 0}}}, []string{"a", "d", "c"}},
		{"2015 to 2020", Query{Ranges: map[string][2]float64{"year": {2015, 2020}}}, []string{"a", "b", "c"}},
		{"price 6000 to 10 000", Query{MinPrice: 6000, MaxPrice: 10000}, []string{"d", "b"}},
		{"read only", Query{DetailedOnly: true}, []string{"a", "d", "b"}},
		{"cheapest", Query{Sort: "price"}, []string{"a", "d", "b", "c", "huge"}},
		{"newest", Query{Sort: "newest"}, []string{"d", "a", "c", "b", "huge"}},
		{"furthest below", Query{Sort: "discount"}, []string{"a", "d", "b", "c", "huge"}},
	} {
		p, err := svc.List(ctx, "site", "things", tt.q)
		var got []string
		for _, g := range p.Items {
			got = append(got, g.ExternalID)
		}
		if err != nil || !slices.Equal(got, tt.want) || p.Total != len(tt.want) {
			t.Errorf("%s: %v (total %d, err %v), want %v", tt.name, got, p.Total, err, tt.want)
		}
	}
	for _, q := range []Query{
		{Buy: "maybe"}, {Sort: "colour"}, {Choices: map[string][]string{"year": {"2020"}}},
		{Ranges: map[string][2]float64{"colour": {1, 0}}}, {Choices: map[string][]string{"smell": {"x"}}},
	} {
		if _, err := svc.List(ctx, "site", "things", q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%+v: err = %v, want ErrBadQuery", q, err)
		}
	}
	if _, err := svc.List(ctx, "site", "flats", Query{}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("unknown category: err = %v", err)
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

func cheapThings(n int) []events.ListingDiscovered {
	var ads []events.ListingDiscovered
	for i := range n {
		ads = append(ads, thing("t"+strconv.Itoa(i), 5000+float64(i)*50, "red", 2020))
	}
	return ads
}

// A site pushing back on ad pages is given longer pauses, then left alone.
func TestScanBacksOff(t *testing.T) {
	svc := newTestService(t)
	site := &fakeSite{pages: [][]events.ListingDiscovered{cheapThings(8)}, enrichErr: statusErr(429)}
	pace := &pacing{}
	scan := runScan(t, svc, newScanner(svc, site, newStore(), pace, 10))
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

// An ad whose page says it was removed is marked sold and leaves the list.
func TestScanDropsRemoved(t *testing.T) {
	svc := newTestService(t)
	site := &fakeSite{pages: [][]events.ListingDiscovered{cheapThings(6)}, removed: map[string]bool{"t2": true}}
	store := newStore()
	scan := runScan(t, svc, newScanner(svc, site, store, &pacing{}, 10))
	if scan.Status != "done" || scan.Gone != 1 || scan.Rated != 5 || store.ads["t2"].Active {
		t.Errorf("scan = %+v, t2 active %v; want 1 gone, 5 rated", scan, store.ads["t2"].Active)
	}
	page, err := svc.List(context.Background(), "site", "things", Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range page.Items {
		if g.ExternalID == "t2" {
			t.Error("the removed ad is still listed")
		}
	}
}

// A scan that cannot have seen the whole feed marks nothing sold.
func TestScanIncomplete(t *testing.T) {
	svc := newTestService(t)
	storeWith := func(n int) *fakeStore {
		s := newStore()
		for i := range n {
			l := thing("old"+string(rune('a'+i)), 10000, "red", 2020)
			l.Feed = "things/sell"
			s.ads[l.ExternalID] = &Item{Listing: l, Active: true}
		}
		return s
	}
	active := func(s *fakeStore) (n int) {
		for _, c := range s.ads {
			if c.Active {
				n++
			}
		}
		return n
	}

	// An empty first page is a block or a redesign, never the end.
	store := storeWith(3)
	scan := runScan(t, svc, newScanner(svc, &fakeSite{pages: [][]events.ListingDiscovered{{}}}, store, &pacing{}, 10))
	if scan.Status != "failed" || !strings.Contains(scan.Error, "first page") || active(store) != 3 {
		t.Errorf("empty first page: %+v, %d active; want failed, nothing sold", scan, active(store))
	}

	// Capped before the end: rated, but sold ones untouched, and said so.
	store = storeWith(3)
	site := &fakeSite{pages: [][]events.ListingDiscovered{{thing("a", 9000, "red", 2020)}, {thing("b", 9100, "red", 2020)}}}
	scan = runScan(t, svc, newScanner(svc, site, store, &pacing{}, 1))
	if scan.Status != "done" || scan.Complete || !strings.Contains(scan.Note, "stopped after 1 pages") || active(store) != 4 {
		t.Errorf("capped: %+v, %d active; want done, incomplete, nothing sold", scan, active(store))
	}

	// An empty page mid-feed ends the crawl early: most stored ones look
	// gone, which is implausible, so none is marked.
	store = storeWith(5)
	site = &fakeSite{pages: [][]events.ListingDiscovered{{thing("a", 9000, "red", 2020)}, {}}}
	scan = runScan(t, svc, newScanner(svc, site, store, &pacing{}, 10))
	if scan.Complete || !strings.Contains(scan.Note, "too many") || scan.Gone != 0 || active(store) != 6 {
		t.Errorf("empty mid-feed: %+v, %d active; want nothing sold", scan, active(store))
	}

	// A plausible share gone is marked, and the scan is complete.
	store = storeWith(1)
	site = &fakeSite{pages: [][]events.ListingDiscovered{{thing("a", 9000, "red", 2020), thing("b", 9100, "red", 2020), thing("c", 9200, "red", 2020)}}}
	scan = runScan(t, svc, newScanner(svc, site, store, &pacing{}, 10))
	if !scan.Complete || scan.Note != "" || scan.Gone != 1 {
		t.Errorf("normal: %+v, want complete with 1 sold", scan)
	}
}

// A scraper starting up marks the scans it left running as interrupted, so
// a new one can be requested at once.
func TestRunInterruptsLeftovers(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.RequestScan(ctx, "site", "things"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.q.ClaimScan(ctx, []string{"site"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestScan(ctx, "site", "things"); !errors.Is(err, ErrBusy) {
		t.Fatalf("while running: err = %v, want ErrBusy", err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	newScanner(svc, &fakeSite{pages: [][]events.ListingDiscovered{{}}}, newStore(), &pacing{}, 10).Run(short)
	scan, _, err := svc.LatestScan(ctx, "site", "things")
	if err != nil || scan.Status != "failed" || !strings.Contains(scan.Error, "interrupted") {
		t.Errorf("left running: %+v (err %v), want failed as interrupted", scan, err)
	}
	if _, err := svc.RequestScan(ctx, "site", "things"); err != nil {
		t.Errorf("request after the restart: %v", err)
	}
}
