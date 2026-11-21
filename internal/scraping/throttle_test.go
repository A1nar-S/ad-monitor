package scraping

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Requests from several goroutines are spaced by Gap between any two.
func TestThrottle(t *testing.T) {
	th := &Throttle{Gap: 20 * time.Millisecond}
	var mu sync.Mutex
	var at []time.Time
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if th.Wait(context.Background()) {
				mu.Lock()
				at = append(at, time.Now())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(at) != 4 {
		t.Fatalf("%d requests went, want 4", len(at))
	}
	first, last := at[0], at[0]
	for _, a := range at {
		if a.Before(first) {
			first = a
		}
		if a.After(last) {
			last = a
		}
	}
	if span := last.Sub(first); span < 3*th.Gap-5*time.Millisecond {
		t.Errorf("4 requests within %v, want at least 3 gaps of %v", span, th.Gap)
	}

	// A cancelled wait gives up.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if th.Wait(ctx) {
		t.Error("wait went ahead after its context ended")
	}
}
