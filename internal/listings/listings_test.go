package listings

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// Needs a real Postgres: TEST_DATABASE_URL=postgres://... go test ./internal/listings
func newTestService(t *testing.T) (*Service, *pgxpool.Pool) {
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

	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS listings, listings_outbox, feed_state, listings_migrations`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, DefaultSeedWindow)
	for i := 0; i < 2; i++ { // second run proves migrations are idempotent
		if err := svc.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return svc, pool
}

func listing(id, feed string) events.ListingDiscovered {
	price := 300.0
	return events.ListingDiscovered{
		Source: "ss.com", ExternalID: id, Title: "Flat", URL: "https://x/" + id, Feed: feed,
		Attrs: map[string]any{"rooms": 2.0}, PriceValue: &price, Price: "300 €/mēn.", PostedAt: time.Now(),
	}
}

func TestIngestUpsert(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()

	l := listing("1", "")
	if isNew, err := svc.Ingest(ctx, l); err != nil || !isNew {
		t.Fatalf("first ingest: new=%v err=%v", isNew, err)
	}
	l.Title = "Flat (renamed)"
	if isNew, err := svc.Ingest(ctx, l); err != nil || isNew {
		t.Fatalf("second ingest: new=%v err=%v", isNew, err)
	}

	var title string
	var seenLater bool
	if err := pool.QueryRow(ctx,
		`SELECT title, last_seen_at > first_seen_at FROM listings WHERE external_id = '1'`).Scan(&title, &seenLater); err != nil {
		t.Fatal(err)
	}
	if title != "Flat (renamed)" || !seenLater {
		t.Errorf("title=%q seenLater=%v", title, seenLater)
	}
}

// The first feed sticks, but a narrower one that sees the ad again replaces
// it: an ad from the all-deals car feed later seen on the sale feed is a sale.
func TestFeedNarrows(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	for _, step := range []struct{ seenOn, want string }{
		{"transport/cars", "transport/cars"},
		{"transport/cars/sell", "transport/cars/sell"},
		{"transport/cars", "transport/cars/sell"},     // broader again: kept
		{"transport/cars/buy", "transport/cars/sell"}, // a sibling: kept
		{"transport/carsx/sell", "transport/cars/sell"},
	} {
		if _, err := svc.Ingest(ctx, listing("1", step.seenOn)); err != nil {
			t.Fatal(err)
		}
		var feed string
		if err := pool.QueryRow(ctx, `SELECT feed FROM listings WHERE external_id = '1'`).Scan(&feed); err != nil {
			t.Fatal(err)
		}
		if feed != step.want {
			t.Errorf("after %s: feed %q, want %q", step.seenOn, feed, step.want)
		}
	}
}

func TestSeedingAndOutbox(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()

	// A feed's first listings are stored but not announced.
	if _, err := svc.Ingest(ctx, listing("1", "riga/centre/hand_over")); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM listings_outbox`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("seed window: outbox has %d rows, want 0", queued)
	}

	// Once the feed is old enough, new listings are queued exactly once.
	if _, err := pool.Exec(ctx, `UPDATE feed_state SET first_seen_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.Ingest(ctx, listing("2", "riga/centre/hand_over")); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM listings_outbox`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("outbox has %d rows, want 1", queued)
	}

	var got []string
	n, err := svc.RelayOutbox(ctx, 10, func(_ context.Context, l events.ListingDiscovered) error {
		got = append(got, l.ExternalID)
		return nil
	})
	if err != nil || n != 1 || len(got) != 1 || got[0] != "2" {
		t.Fatalf("relay: n=%d got=%v err=%v", n, got, err)
	}
	if n, _ := svc.RelayOutbox(ctx, 10, func(context.Context, events.ListingDiscovered) error { return nil }); n != 0 {
		t.Errorf("second relay published %d, want 0", n)
	}
}

func TestPurgeKeepsRecentlySeen(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()

	for _, id := range []string{"old", "kept"} {
		if _, err := svc.Ingest(ctx, listing(id, "")); err != nil {
			t.Fatal(err)
		}
	}
	// "old" was first seen long ago and not since; "kept" is old but still on a feed.
	if _, err := pool.Exec(ctx, `UPDATE listings SET first_seen_at = now() - interval '30 days',
		last_seen_at = now() - interval '10 days' WHERE external_id = 'old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE listings SET first_seen_at = now() - interval '30 days' WHERE external_id = 'kept'`); err != nil {
		t.Fatal(err)
	}

	n, err := svc.Purge(ctx, 7*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	var left string
	if err := pool.QueryRow(ctx, `SELECT external_id FROM listings`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != "kept" {
		t.Errorf("left = %q, want kept", left)
	}
}

func TestDetailsSurviveScrapes(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	l := listing("1", "riga/centre/hand_over")
	if _, err := svc.Ingest(ctx, l); err != nil {
		t.Fatal(err)
	}
	enriched := l
	enriched.Attrs = map[string]any{"rooms": 2.0, "house_type": "Ķieģeļu"}
	if err := svc.SaveDetails(ctx, l, enriched); err != nil {
		t.Fatal(err)
	}
	// The next scrape rewrites attrs from the list page and must not lose
	// what the ad page added, nor the feed the ad was first found on.
	l.Attrs = map[string]any{"rooms": 3.0}
	l.Feed = "riga/all/hand_over"
	if _, err := svc.Ingest(ctx, l); err != nil {
		t.Fatal(err)
	}

	recent, err := svc.Recent(ctx, "ss.com", 10)
	if err != nil || len(recent) != 1 {
		t.Fatalf("recent = %v, %v", recent, err)
	}
	got := recent[0]
	if got.Listing.Attrs["house_type"] != "Ķieģeļu" || got.Listing.Attrs["rooms"] != 3.0 {
		t.Errorf("attrs = %v, want the ad page's house_type and the feed's new rooms", got.Listing.Attrs)
	}
	if got.Listing.Feed != "riga/centre/hand_over" {
		t.Errorf("feed = %q, want the first one", got.Listing.Feed)
	}

	one, err := svc.Get(ctx, got.ID)
	if err != nil || one.DetailedAt == nil || one.Listing.ExternalID != "1" {
		t.Fatalf("get = %+v, %v", one, err)
	}
	if err := svc.MarkGone(ctx, "ss.com", "1"); err != nil {
		t.Fatal(err)
	}
	if one, _ = svc.Get(ctx, got.ID); one.Active {
		t.Error("still active after MarkGone")
	}
	if _, err := svc.Get(ctx, got.ID+100); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing listing: err = %v, want ErrNotFound", err)
	}
}

func TestCarPeers(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	car := func(id, model, fuel string, year float64, km any, price float64) events.ListingDiscovered {
		attrs := map[string]any{"make": "BMW", "model": model, "year": year, "fuel": fuel}
		if km != nil {
			attrs["mileage_km"] = km
		}
		return events.ListingDiscovered{
			Source: "ss.com", ExternalID: id, Title: "BMW", URL: "https://x/" + id,
			Feed: "transport/cars/bmw/sell", Location: "transport/cars/bmw/" + model,
			Attrs: attrs, PriceValue: &price, Price: "€",
		}
	}
	for _, l := range []events.ListingDiscovered{
		car("self", "520", "diesel", 2016, 180000.0, 9000),
		car("a", "520", "diesel", 2015, 200000.0, 10000),
		car("b", "520", "diesel", 2018, nil, 14000),     // no mileage: 0
		car("c", "520", "diesel", 2019, 90000.0, 18000), // out of years
		car("d", "520", "petrol", 2016, 150000.0, 11000),
		car("e", "530", "diesel", 2016, 150000.0, 12000),
		car("f", "520", "diesel", 2017, "?", 12500), // odd mileage: unknown, not an error
	} {
		if _, err := svc.Ingest(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	noPrice := car("g", "520", "diesel", 2016, 1.0, 0)
	noPrice.PriceValue = nil
	if _, err := svc.Ingest(ctx, noPrice); err != nil {
		t.Fatal(err)
	}
	// Ad "a"'s page was read: its equipment comes from the details.
	before := car("a", "520", "diesel", 2015, 200000.0, 10000)
	read := before
	read.Attrs = map[string]any{"equipment": []any{"cruise", "xenon"}}
	for k, v := range before.Attrs {
		read.Attrs[k] = v
	}
	if err := svc.SaveDetails(ctx, before, read); err != nil {
		t.Fatal(err)
	}

	peers, err := svc.CarPeers(ctx, PeerQuery{Source: "ss.com", Make: "BMW", Model: "520", YearMin: 2014, YearMax: 2018, Exclude: "self"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[float64]Peer{}
	for _, p := range peers {
		got[p.Price] = p
	}
	at := func(price float64, year int, km float64, fuel string, eq ...string) Peer {
		return Peer{Feed: "transport/cars/bmw/sell", Location: "transport/cars/bmw/520", Price: price, Year: year, MileageKm: km, Fuel: fuel, Equipment: eq}
	}
	want := map[float64]Peer{
		10000: at(10000, 2015, 200000, "diesel", "cruise", "xenon"),
		14000: at(14000, 2018, 0, "diesel"),
		12500: at(12500, 2017, 0, "diesel"),
		11000: at(11000, 2016, 150000, "petrol"),
	}
	if len(got) != len(want) || len(peers) != len(want) {
		t.Fatalf("peers = %+v, want %+v", peers, want)
	}
	for k, w := range want {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("peer at %v = %+v, want %+v", k, got[k], w)
		}
	}

	// The car's own earlier ad (a repost) is no peer.
	earlier := car("h", "520", "diesel", 2016, 180000.0, 9500)
	earlier.Fingerprint = "car|sell|bmw|520|2016"
	if _, err := svc.Ingest(ctx, earlier); err != nil {
		t.Fatal(err)
	}
	q := PeerQuery{Source: "ss.com", Make: "BMW", Model: "520", YearMin: 2014, YearMax: 2018, Exclude: "self"}
	if all, _ := svc.CarPeers(ctx, q); len(all) != 5 {
		t.Errorf("%d peers, want 5 with the earlier ad", len(all))
	}
	q.Fingerprint = earlier.Fingerprint
	if mine, _ := svc.CarPeers(ctx, q); len(mine) != 4 {
		t.Errorf("%d peers, want 4: its own earlier ad left out", len(mine))
	}
}

func TestReposts(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()

	ad := func(id string, price float64) events.ListingDiscovered {
		l := listing(id, "") // no feed: announced at once
		l.Fingerprint, l.PriceValue, l.Price = "car|sell|bmw|520|2016|2|diesel|189000", &price, "€"
		return l
	}
	announced := func() []events.ListingDiscovered {
		t.Helper()
		var out []events.ListingDiscovered
		if _, err := svc.RelayOutbox(ctx, 10, func(_ context.Context, l events.ListingDiscovered) error {
			out = append(out, l)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	for _, l := range []events.ListingDiscovered{ad("1", 9000), listing("other", "")} {
		if _, err := svc.Ingest(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if got := announced(); len(got) != 2 || got[0].RepostOf != nil || got[1].RepostOf != nil {
		t.Fatalf("first sight: %+v, want two plain new ads", got)
	}
	var firstSeen time.Time
	if err := pool.QueryRow(ctx, `SELECT first_seen_at FROM listings WHERE external_id = '1'`).Scan(&firstSeen); err != nil {
		t.Fatal(err)
	}

	// Deleted and posted again, cheaper: a new ID, the same item.
	if _, err := svc.Ingest(ctx, ad("2", 8500)); err != nil {
		t.Fatal(err)
	}
	got := announced()
	if len(got) != 1 || got[0].RepostOf == nil {
		t.Fatalf("repost: %+v, want one announced as a repost", got)
	}
	r := got[0].RepostOf
	if r.ExternalID != "1" || r.Times != 1 || r.PriceValue == nil || *r.PriceValue != 9000 || !r.FirstListed.Equal(firstSeen) {
		t.Errorf("repost of = %+v, want ad 1 at 9000 first seen %v", r, firstSeen)
	}

	// A third time: the latest earlier ad, but the item's first sighting.
	if _, err := svc.Ingest(ctx, ad("3", 8000)); err != nil {
		t.Fatal(err)
	}
	if got := announced(); len(got) != 1 || got[0].RepostOf.ExternalID != "2" || got[0].RepostOf.Times != 2 || !got[0].RepostOf.FirstListed.Equal(firstSeen) {
		t.Errorf("second repost = %+v", got)
	}

	// Seeing an ad again is no repost, and an ad without a fingerprint
	// keeps the one it had.
	again := ad("1", 9000)
	again.Fingerprint = ""
	if _, err := svc.Ingest(ctx, again); err != nil {
		t.Fatal(err)
	}
	if got := announced(); len(got) != 0 {
		t.Errorf("known ad announced: %+v", got)
	}
	var fp string
	if err := pool.QueryRow(ctx, `SELECT fingerprint FROM listings WHERE external_id = '1'`).Scan(&fp); err != nil || fp == "" {
		t.Errorf("fingerprint after a scrape without one = %q (err %v), want it kept", fp, err)
	}
}

// A full scan stores ads without announcing them, yet a scanned ad's first
// regular sight is still new: the scan neither floods nor swallows alerts.
func TestStoreScanned(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	outbox := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM listings_outbox`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	known := listing("known", "")
	if _, err := svc.Ingest(ctx, known); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, l := range []events.ListingDiscovered{listing("scanned", ""), known} {
		if err := svc.StoreScanned(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if n := outbox(); n != 1 {
		t.Fatalf("outbox has %d, want only the regular ingest's 1", n)
	}
	ids, err := svc.KnownIDs(ctx, "ss.com", []string{"known", "scanned"})
	if err != nil || !ids["known"] || ids["scanned"] {
		t.Errorf("known = %v (err %v), want only the regularly seen ad", ids, err)
	}

	// The feeds see it: new, announced once; the known ad stays known.
	if isNew, err := svc.Ingest(ctx, listing("scanned", "")); err != nil || !isNew {
		t.Errorf("first regular sight: new=%v err=%v, want new", isNew, err)
	}
	if isNew, _ := svc.Ingest(ctx, listing("scanned", "")); isNew {
		t.Error("second regular sight is new again")
	}
	if isNew, _ := svc.Ingest(ctx, known); isNew {
		t.Error("a scan made a known ad new")
	}
	if n := outbox(); n != 2 {
		t.Errorf("outbox has %d, want 2", n)
	}

	// After the scan: the ad it did not see is gone.
	if _, err := pool.Exec(ctx, `UPDATE listings SET last_seen_at = now() - interval '1 hour' WHERE external_id = 'known'`); err != nil {
		t.Fatal(err)
	}
	unseen, err := svc.UnseenSince(ctx, "ss.com", start)
	if err != nil || len(unseen) != 1 || unseen[0].ExternalID != "known" {
		t.Fatalf("unseen = %+v (err %v), want the known ad", unseen, err)
	}
	if n, err := svc.MarkGoneIDs(ctx, "ss.com", []string{"known"}); err != nil || n != 1 {
		t.Errorf("marked %d gone (err %v), want 1", n, err)
	}
	cars, err := svc.StoredCars(ctx, "ss.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(cars) != 0 {
		t.Errorf("stored cars = %d, want 0: the test ads have no make", len(cars))
	}
}
