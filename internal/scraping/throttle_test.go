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

// Jitter stretches each gap by up to its share, never shrinks it.
func TestJittered(t *testing.T) {
	gap := time.Second
	for range 1000 {
		if d := Jittered(gap, 0.5); d < gap || d > gap*3/2 {
			t.Fatalf("Jittered(1s, 0.5) = %v, want within [1s, 1.5s]", d)
		}
	}
	if d := Jittered(gap, 0); d != gap {
		t.Errorf("no jitter: %v", d)
	}
}

func TestThrottleJitter(t *testing.T) {
	th := &Throttle{Gap: 10 * time.Millisecond, Jitter: 1}
	th.Wait(context.Background())
	start := time.Now()
	th.Wait(context.Background())
	if d := time.Since(start); d < 9*time.Millisecond || d > 40*time.Millisecond {
		t.Errorf("second request after %v, want within [10ms, 20ms] (with slack)", d)
	}
}
