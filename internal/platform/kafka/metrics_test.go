package kafka

import (
	"time"
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHandleObservedCountsRetries(t *testing.T) {
	const topic = "metrics-test"
	retries := handleRetries.WithLabelValues(topic)
	before := testutil.ToFloat64(retries)

	calls := 0
	err := handleObserved(context.Background(), topic, time.Millisecond, 1, func(context.Context, int) error {
		if calls++; calls == 1 {
			return errors.New("transient") // retried after retryDelay
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := testutil.ToFloat64(retries) - before; d != 1 {
		t.Errorf("retries rose by %v, want 1", d)
	}
	if n := testutil.CollectAndCount(handleDuration); n == 0 {
		t.Error("no handler duration recorded")
	}
}
