package scraping

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

type statusErr int

func (e statusErr) Error() string   { return "status" }
func (e statusErr) HTTPStatus() int { return int(e) }

func TestFetchResult(t *testing.T) {
	for _, c := range []struct {
		page int
		n    int
		err  error
		want string
	}{
		{1, 30, nil, "ok"}, {1, 0, nil, "empty"}, {2, 0, nil, "ok"}, {1, 0, errors.New("dns"), "failed"},
		{1, 0, statusErr(429), "blocked"}, {1, 0, statusErr(503), "blocked"}, {1, 0, statusErr(404), "failed"},
	} {
		if got := fetchResult(c.page, c.n, c.err); got != c.want {
			t.Errorf("fetchResult(%d, %d, %v) = %q, want %q", c.page, c.n, c.err, got, c.want)
		}
	}
}

// Counters are global, so assert on how much they rose, not their value.
func TestRunnerCountsFetchesAndListings(t *testing.T) {
	f := &fakeFetcher{pages: map[string][][]string{"a": pages("1 2 3")}}
	var published []events.ListingDiscovered
	r := newRunner(f, nil, &published)
	ok := feedFetches.WithLabelValues("ss.com", "ok")
	found := listingsDiscovered.WithLabelValues("ss.com")
	okBefore, foundBefore := testutil.ToFloat64(ok), testutil.ToFloat64(found)

	if err := r.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := testutil.ToFloat64(ok) - okBefore; d != 1 {
		t.Errorf("feed fetches rose by %v, want 1", d)
	}
	if d := testutil.ToFloat64(found) - foundBefore; d != 3 {
		t.Errorf("listings discovered rose by %v, want 3", d)
	}
}

func TestObserveCycleSetsLastSuccessOnlyOnSuccess(t *testing.T) {
	// A source name unique to this run: the series are global.
	src := fmt.Sprintf("test-source-%d", time.Now().UnixNano())
	observeCycle(src, time.Now(), false, errors.New("boom"))
	if got := testutil.ToFloat64(lastSuccess.WithLabelValues(src)); got != 0 {
		t.Fatalf("last success after a failure = %v, want 0", got)
	}
	observeCycle(src, time.Now(), false, nil)
	if got := testutil.ToFloat64(lastSuccess.WithLabelValues(src)); got < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Errorf("last success not set after a good cycle: %v", got)
	}
	if got := testutil.ToFloat64(cycles.WithLabelValues(src, "failed")); got != 1 {
		t.Errorf("failed cycles = %v, want 1", got)
	}
	if got := testutil.ToFloat64(cycles.WithLabelValues(src, "ok")); got != 1 {
		t.Errorf("ok cycles = %v, want 1", got)
	}
}
