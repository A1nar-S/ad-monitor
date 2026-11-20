package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInitConsumeSeries(t *testing.T) {
	initConsumeSeries("init.test")
	// Reading a series with WithLabelValues would create it, so count what
	// exists: 2 consumed + 2 dead-lettered + 2 dlq publishes for this topic.
	before := testutil.CollectAndCount(consumed) + testutil.CollectAndCount(deadLettered) + testutil.CollectAndCount(dlqPublishes)
	if before < 6 {
		t.Fatalf("series after init = %d, want >= 6", before)
	}
}

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
