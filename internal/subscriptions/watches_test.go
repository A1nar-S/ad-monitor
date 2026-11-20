package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

func TestWatchThreshold(t *testing.T) {
	below := WatchRule{Kind: RuleBelow, TargetCents: ptr(int64(25000))}
	if got := below.Threshold(99999); got != 25000 {
		t.Errorf("below: %d", got)
	}
	// The roadmap's example: EUR 100 with a 5% rule alerts at EUR 95 or less.
	drop := WatchRule{Kind: RuleDrop, DropPercent: ptr(5.0)}
	if got := drop.Threshold(10000); got != 9500 {
		t.Errorf("5%% of 100.00: %d", got)
	}
	if got := (WatchRule{Kind: RuleDrop, DropPercent: ptr(12.5)}).Threshold(129900); got != 113662 {
		t.Errorf("12.5%% of 1299.00: %d", got) // 1136.625 rounds down: never alert above the rule
	}
}

// fakeReader answers reads from a queue of readings (or errors); the last
// one repeats.
type fakeReader struct {
	queue []any
	reads int
}

func (f *fakeReader) ReadPrice(context.Context, string, sources.Extractor) (sources.Reading, error) {
	f.reads++
	next := f.queue[0]
	if len(f.queue) > 1 {
		f.queue = f.queue[1:]
	}
	if err, ok := next.(error); ok {
		return sources.Reading{}, err
	}
	return sources.Reading{Price: sources.Price{Cents: next.(int64), Currency: "EUR"}, Raw: "x", Title: "Boden"}, nil
}

func (f *fakeReader) then(vs ...any) { f.queue = vs }

const guitar = "https://strandberg.example/boden"

var priceSpan = sources.Extractor{Selector: "p > span.text-xl"}

func eur(cents int64) *sources.Reading {
	return &sources.Reading{Price: sources.Price{Cents: cents, Currency: "EUR"}, Raw: "1 299 €", Title: "Boden"}
}

// due makes every target due now, as if its interval had passed.
func due(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE watch_targets SET next_check_at = now()`); err != nil {
		t.Fatal(err)
	}
}

func TestWatchLifecycle(t *testing.T) {
	svc, pool := newTestService(t, fakeSource{})
	svc.WithWatchLimits(2, 30).WithPublicURL("https://ads.example")
	ctx := context.Background()
	ana, anaDM := user(t, svc, "ana")
	ben, benDM := user(t, svc, "ben")

	// Ana: alert at or below EUR 1,200. Ben: alert after a 5% drop from the
	// price when he started (1,299.00 → threshold 1,234.05).
	a, err := svc.CreateWatch(ctx, ana, WatchInput{
		Name: "Boden", URL: guitar, Extractor: priceSpan, IntervalMinutes: 60, ChannelID: anaDM.ID,
		Rule: WatchRule{Kind: RuleBelow, TargetCents: ptr(int64(120000))}, Reading: eur(129900),
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateWatch(ctx, ben, WatchInput{
		Name: "Guitar", URL: guitar, Extractor: priceSpan, IntervalMinutes: 30, ChannelID: benDM.ID,
		Rule: WatchRule{Kind: RuleDrop, DropPercent: ptr(5.0)}, Reading: eur(129900),
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.targetID != b.targetID {
		t.Fatal("the same page and element must be one shared target")
	}
	if a.Below || b.Below || a.ThresholdCents != 120000 || b.ThresholdCents != 123405 || *a.PriceCents != 129900 {
		t.Fatalf("baseline: %+v / %+v", a, b)
	}

	r := &fakeReader{}
	check := func(want int, vs ...any) {
		t.Helper()
		r.then(vs...)
		due(t, pool)
		n, err := svc.CheckDueWatches(ctx, r, 10)
		if err != nil || n != want {
			t.Fatalf("checked %d targets (want %d): %v", n, want, err)
		}
	}
	var sent []events.NotificationRequested
	relay := func() []events.NotificationRequested {
		t.Helper()
		before := len(sent)
		_, err := svc.RelayWatchAlerts(ctx, 100, func(_ context.Context, n events.NotificationRequested) error {
			sent = append(sent, n)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return sent[before:]
	}

	check(1, int64(125000)) // above both thresholds
	if got := relay(); len(got) != 0 {
		t.Fatalf("no alert above the thresholds: %v", got)
	}
	check(1, int64(119900)) // below both: one alert each
	got := relay()
	if len(got) != 2 || got[0].Channel.Target != "dm-ana" || got[1].Channel.Target != "dm-ben" {
		t.Fatalf("drop alerts: %+v", got)
	}
	if !strings.Contains(got[0].Text, "€1,199") || !strings.Contains(got[0].Text, "(was €1,250)") || !strings.Contains(got[0].Text, "target of €1,200") {
		t.Errorf("ana's alert: %s", got[0].Text)
	}
	if !strings.Contains(got[1].Text, "7.7% below your reference price of €1,299 (you asked for 5%)") {
		t.Errorf("ben's alert: %s", got[1].Text)
	}
	if got[0].Key == "" || got[0].Key == got[1].Key {
		t.Errorf("keys: %q %q", got[0].Key, got[1].Key)
	}

	check(1, int64(119000)) // still below: no repeat
	if got := relay(); len(got) != 0 {
		t.Fatalf("repeated while below: %v", got)
	}
	check(1, int64(130000)) // back above: re-armed, silently
	check(1, int64(118000)) // below again: alerts again
	if got := relay(); len(got) != 2 {
		t.Fatalf("after re-arming: %v", got)
	}

	// A price under half the last one is read twice; disagreeing reads are
	// a failure, never an alert, and the last valid price stays.
	check(1, int64(50000), int64(118000))
	if got := relay(); len(got) != 0 {
		t.Fatalf("misread alerted: %v", got)
	}
	a, _ = svc.GetWatch(ctx, ana, a.ID)
	if *a.PriceCents != 118000 || a.Failures != 1 || !strings.Contains(a.LastError, "two reads disagree") {
		t.Fatalf("after a misread: %d failures, %q, price %d", a.Failures, a.LastError, *a.PriceCents)
	}

	// Two more failures make three in a row: both watchers hear once.
	check(1, fmt.Errorf("fetch: %w", sources.ErrBlocked))
	check(1, fmt.Errorf("fetch: %w", sources.ErrBlocked))
	got = relay()
	if len(got) != 2 || !strings.Contains(got[0].Text, "the site refuses the request") ||
		!strings.Contains(got[0].Text, "https://ads.example/watches/") || !strings.Contains(got[0].Text, "last price €1,180") {
		t.Fatalf("failing alerts: %+v", got)
	}
	check(1, fmt.Errorf("fetch: %w", sources.ErrBlocked))
	if got := relay(); len(got) != 0 {
		t.Fatalf("failing told twice: %v", got)
	}
	a, _ = svc.GetWatch(ctx, ana, a.ID)
	if *a.PriceCents != 118000 || !a.Failing {
		t.Fatalf("a failing page keeps its last price: %+v", a)
	}

	// It reads again: recovered, at the same price, so no new drop alert.
	check(1, int64(118000))
	got = relay()
	if len(got) != 2 || !strings.Contains(got[0].Text, "works again") {
		t.Fatalf("recovery: %+v", got)
	}

	// Gradual drops count against the fixed reference: 1,299 → 1,180 was
	// already below Ben's 1,234.05. Resetting makes 1,180 the reference.
	b, err = svc.ResetWatchReference(ctx, ben, b.ID)
	if err != nil || b.ReferenceCents != 118000 || b.Below || b.ThresholdCents != 112100 {
		t.Fatalf("reset: %+v %v", b, err)
	}
	check(1, int64(115000)) // two small drops, neither 5% yet
	check(1, int64(113000))
	check(1, int64(112000)) // 5.1% below 1,180 in total
	if got := relay(); len(got) != 1 || got[0].Channel.Target != "dm-ben" {
		t.Fatalf("gradual drop: %+v", got)
	}

	// Raising the target above the price applies silently.
	a, err = svc.UpdateWatch(ctx, ana, a.ID, WatchInput{
		Name: "Boden", URL: guitar, Extractor: priceSpan, IntervalMinutes: 60,
		Rule: WatchRule{Kind: RuleBelow, TargetCents: ptr(int64(100000))},
	})
	if err != nil || a.Below {
		t.Fatalf("update: %+v %v", a, err)
	}

	history, alerts, err := svc.WatchHistory(ctx, ana, a.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 8 || !history[0].OK || *history[0].Cents != 112000 || len(alerts) != 4 {
		t.Errorf("history: %d points, newest %+v; %d alerts", len(history), history[0], len(alerts))
	}
	if r.reads == 0 {
		t.Fatal("nothing was read")
	}

	// A paused watch is not read; a page nobody watches is gone with its
	// history once the last watch is deleted.
	if err := svc.SetWatchEnabled(ctx, ana, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWatchEnabled(ctx, ben, b.ID, false); err != nil {
		t.Fatal(err)
	}
	check(0, int64(1))
	if _, err := svc.GetWatch(ctx, ben, a.ID); !errors.Is(err, ErrWatchNotFound) {
		t.Errorf("another user's watch: %v", err)
	}
	for _, w := range []struct {
		o  Owner
		id int64
	}{{ana, a.ID}, {ben, b.ID}} {
		if err := svc.DeleteWatch(ctx, w.o, w.id); err != nil {
			t.Fatal(err)
		}
	}
	var targets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM watch_targets`).Scan(&targets); err != nil || targets != 0 {
		t.Errorf("targets left: %d %v", targets, err)
	}
}

func TestWatchValidation(t *testing.T) {
	svc, pool := newTestService(t, fakeSource{})
	svc.WithWatchLimits(1, 60)
	ctx := context.Background()
	ana, dm := user(t, svc, "ana")
	ok := WatchInput{
		Name: "Boden", URL: guitar, Extractor: priceSpan, IntervalMinutes: 60, ChannelID: dm.ID,
		Rule: WatchRule{Kind: RuleBelow, TargetCents: ptr(int64(1000))}, Reading: eur(129900),
	}
	for name, mod := range map[string]func(*WatchInput){
		"no reading":     func(in *WatchInput) { in.Reading = nil },
		"no currency":    func(in *WatchInput) { in.Reading = &sources.Reading{Price: sources.Price{Cents: 100}} },
		"no element":     func(in *WatchInput) { in.Extractor = sources.Extractor{} },
		"short interval": func(in *WatchInput) { in.IntervalMinutes = 30 },
		"no rule":        func(in *WatchInput) { in.Rule = WatchRule{} },
		"drop of 100%":   func(in *WatchInput) { in.Rule = WatchRule{Kind: RuleDrop, DropPercent: ptr(100.0)} },
		"not a web page": func(in *WatchInput) { in.URL = "ftp://x" },
		"no name":        func(in *WatchInput) { in.Name = " " },
	} {
		in := ok
		mod(&in)
		var inv *sources.InvalidSearch
		if _, err := svc.CreateWatch(ctx, ana, in); !errors.As(err, &inv) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	w, err := svc.CreateWatch(ctx, ana, ok)
	if err != nil {
		t.Fatal(err)
	}
	var inv *sources.InvalidSearch
	if _, err := svc.CreateWatch(ctx, ana, ok); !errors.As(err, &inv) || !strings.Contains(inv.Msg, "limit") {
		t.Errorf("over the limit: %v", err)
	}

	// Picking another element restarts the watch from its new reading and
	// drops the old page if nobody else watches it.
	moved := ok
	moved.Extractor = sources.Extractor{Selector: `input[name="cartItemPriceDisplayIncVat"]`, Attr: "value", Currency: "EUR"}
	moved.Reading = nil
	if _, err := svc.UpdateWatch(ctx, ana, w.ID, moved); !errors.As(err, &inv) {
		t.Errorf("moved without a reading: %v", err)
	}
	moved.Reading = eur(119900)
	got, err := svc.UpdateWatch(ctx, ana, w.ID, moved)
	if err != nil || got.ReferenceCents != 119900 || got.Extractor != moved.Extractor || got.targetID == w.targetID {
		t.Fatalf("moved: %+v %v", got, err)
	}
	var targets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM watch_targets`).Scan(&targets); err != nil || targets != 1 {
		t.Errorf("targets: %d %v", targets, err)
	}

	// Check now reads at once, but not twice within half a minute.
	if _, err := pool.Exec(ctx, `UPDATE watch_targets SET checked_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	r := &fakeReader{queue: []any{int64(99900)}}
	if v, err := svc.CheckWatchNow(ctx, ana, w.ID, r); err != nil || *v.PriceCents != 99900 || v.Below {
		t.Fatalf("check now: %+v %v", v, err)
	}
	if _, err := svc.CheckWatchNow(ctx, ana, w.ID, r); err != nil || r.reads != 1 {
		t.Errorf("second check now read again: %d reads, %v", r.reads, err)
	}
}
