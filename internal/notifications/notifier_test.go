package notifications

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

type fake struct {
	sent     []string
	fail     bool
	failText bool
	// textsLeft, when set, is how many more texts succeed before failing.
	textsLeft *int
}

func (f *fake) Kind() string { return "fake" }
func (f *fake) NotifyListing(_ context.Context, target string, l events.ListingDiscovered) error {
	if f.fail {
		return errors.New("boom")
	}
	f.sent = append(f.sent, target+":"+l.ExternalID)
	return nil
}
func (f *fake) NotifyText(_ context.Context, target, msg string) error {
	if f.failText {
		return errors.New("boom")
	}
	if f.textsLeft != nil {
		if *f.textsLeft == 0 {
			return errors.New("boom")
		}
		*f.textsLeft--
	}
	f.sent = append(f.sent, target+":"+msg)
	return nil
}

// Needs a real Postgres: TEST_DATABASE_URL=postgres://... go test ./internal/notifications
func TestHandleIsIdempotent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS digest_queue, deliveries, notifications_migrations`); err != nil {
		t.Fatal(err)
	}

	f := &fake{}
	svc := NewService(pool, 0, f)
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	req := events.NotificationRequested{
		Key: "1:ss.com:42", Channel: events.Channel{Kind: "fake", Target: "chan"},
		Listing: &events.ListingDiscovered{ExternalID: "42"},
	}

	// A failed send must not burn the key, so the retry delivers.
	f.fail = true
	if err := svc.Handle(ctx, req); err == nil {
		t.Fatal("expected send error")
	}
	f.fail = false
	for i := 0; i < 2; i++ {
		if err := svc.Handle(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sent) != 1 || f.sent[0] != "chan:42" {
		t.Errorf("sent = %v, want exactly one delivery", f.sent)
	}

	// Keyless text (operational alerts) is never deduplicated.
	text := events.NotificationRequested{Channel: req.Channel, Text: "hi"}
	for i := 0; i < 2; i++ {
		if err := svc.Handle(ctx, text); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sent) != 3 {
		t.Errorf("sent = %v, want 3 after two keyless texts", f.sent)
	}
}

func newTestService(t *testing.T, f *fake) (*Service, *pgxpool.Pool) {
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
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS digest_queue, deliveries, notifications_migrations`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, 0, f).WithBurst(5, time.Hour)
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, pool
}

func adRequest(search, id string) events.NotificationRequested {
	return events.NotificationRequested{
		Key: "1:" + search + ":" + id, UserID: 1, SearchName: search,
		Channel: events.Channel{Kind: "fake", Target: "chan"},
		Listing: &events.ListingDiscovered{ExternalID: id, Title: "Car " + id, Price: "1,000  €", URL: "https://x/" + id},
	}
}

func TestBurstGoesToDigest(t *testing.T) {
	f := &fake{}
	svc, pool := newTestService(t, f)
	ctx := context.Background()
	sentCount, digestedCount := deliveries.WithLabelValues("fake", "sent"), deliveries.WithLabelValues("fake", "digested")
	sent0, digested0 := testutil.ToFloat64(sentCount), testutil.ToFloat64(digestedCount)

	// 12 matches for one search: 5 posted, 7 queued. Redelivering one of the
	// queued keys must neither post nor queue it twice.
	for i := range 12 {
		if err := svc.Handle(ctx, adRequest("audi", fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Handle(ctx, adRequest("audi", "11")); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 5 {
		t.Fatalf("sent %d messages, want 5: %v", len(f.sent), f.sent)
	}
	if d := testutil.ToFloat64(sentCount) - sent0; d != 5 {
		t.Errorf("sent counter rose by %v, want 5", d)
	}
	if d := testutil.ToFloat64(digestedCount) - digested0; d != 7 {
		t.Errorf("digested counter rose by %v, want 7", d)
	}

	// Another search has its own budget.
	if err := svc.Handle(ctx, adRequest("bmw", "b1")); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 6 {
		t.Fatalf("other search: sent %d, want 6", len(f.sent))
	}

	// Not due yet: the window has not passed.
	if n, err := svc.FlushDigests(ctx); err != nil || n != 0 {
		t.Fatalf("early flush: n=%d err=%v", n, err)
	}

	// A failed digest stays queued.
	if _, err := pool.Exec(ctx, `UPDATE digest_queue SET queued_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	f.failText = true
	if _, err := svc.FlushDigests(ctx); err == nil {
		t.Fatal("expected digest send error")
	}
	f.failText = false

	n, err := svc.FlushDigests(ctx)
	if err != nil || n != 1 {
		t.Fatalf("flush: n=%d err=%v", n, err)
	}
	if len(f.sent) != 7 {
		t.Fatalf("after flush sent %d, want 7 (one digest)", len(f.sent))
	}
	digest := f.sent[6]
	if !strings.Contains(digest, "7 more matches") || !strings.Contains(digest, "<https://x/11>") {
		t.Errorf("digest = %q", digest)
	}
	if n, _ := svc.FlushDigests(ctx); n != 0 {
		t.Errorf("second flush sent %d digests, want 0", n)
	}
}

// A digest of several messages failing part way sends each ad once: the
// sent messages' ads leave the queue, the rest wait for the next flush.
func TestDigestFailingPartWay(t *testing.T) {
	f := &fake{}
	svc, pool := newTestService(t, f)
	ctx := context.Background()
	for i := range 5 + 25 {
		req := adRequest("audi", fmt.Sprint(i))
		req.Listing.Title = strings.Repeat("a very long title ", 10)
		req.Listing.URL = fmt.Sprintf("https://www.ss.com/msg/lv/transport/cars/audi/a4/%s/ad%03d.html", strings.Repeat("x", 40), i)
		if err := svc.Handle(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE digest_queue SET queued_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	queued := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM digest_queue`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	one := 1
	f.textsLeft = &one
	if _, err := svc.FlushDigests(ctx); err == nil {
		t.Fatal("expected the second message to fail")
	}
	left := queued()
	if left == 0 || left == 25 {
		t.Fatalf("%d of 25 still queued, want those of the unsent messages only", left)
	}
	f.textsLeft = nil
	if n, err := svc.FlushDigests(ctx); err != nil || n != 1 || queued() != 0 {
		t.Fatalf("second flush: n=%d err=%v queued=%d", n, err, queued())
	}
	ads := 0
	for _, s := range f.sent {
		ads += strings.Count(s, "• ")
	}
	if ads != 25 {
		t.Errorf("digest lines = %d, want each of the 25 once", ads)
	}
}

func TestDigestMessagesSplit(t *testing.T) {
	var ls []events.ListingDiscovered
	for i := range 45 {
		ls = append(ls, events.ListingDiscovered{
			Title: strings.Repeat("long title ", 20), Price: "12,345  €",
			URL: fmt.Sprintf("https://www.ss.com/msg/lv/transport/cars/audi/a4/ad%03d.html", i),
		})
	}
	msgs := digestMessages("audi", ls, 5, 10*time.Minute)
	if len(msgs) < 2 {
		t.Fatalf("want several messages, got %d", len(msgs))
	}
	lines := 0
	ads := 0
	for _, m := range msgs {
		if len(m.text) > maxMessageLen {
			t.Errorf("message of %d bytes exceeds the limit", len(m.text))
		}
		lines += strings.Count(m.text, "• ")
		ads += m.ads
	}
	if lines != maxDigestAds || !strings.Contains(msgs[len(msgs)-1].text, "…and 15 more") || ads != 45 {
		t.Errorf("lines=%d, ads=%d, last=%q", lines, ads, msgs[len(msgs)-1].text)
	}
}

func TestRepostText(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	first := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	l := events.ListingDiscovered{PriceValue: price(8500), RepostOf: &events.Repost{FirstListed: first, Times: 1, PriceValue: price(8900), Price: "8 900 €"}}
	if got, want := RepostText(l), "First listed 3 Oct 2026, in 1 earlier ad. Last asked 8 900 €, now 8 500 € (400 € less)."; got != want {
		t.Errorf("cheaper = %q\nwant      %q", got, want)
	}
	l.RepostOf.Times, l.PriceValue = 3, price(8900)
	if got, want := RepostText(l), "First listed 3 Oct 2026, in 3 earlier ads. Same price as before."; got != want {
		t.Errorf("same price = %q\nwant         %q", got, want)
	}
	l.PriceValue = nil
	if got, want := RepostText(l), "First listed 3 Oct 2026, in 3 earlier ads. Last asked 8 900 €."; got != want {
		t.Errorf("no price now = %q\nwant           %q", got, want)
	}
	if RepostText(events.ListingDiscovered{}) != "" {
		t.Error("text for an ad that is no repost")
	}
	l.Title, l.Price = "BMW 520", "8 900 €"
	if line := digestLine(l); line != "• ♻️ **8 900 €** · BMW 520" {
		t.Errorf("digest line = %q, want it marked as a repost", line)
	}
}
