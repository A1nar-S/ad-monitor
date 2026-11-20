package kafka

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/metrics"
)

var (
	published = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_kafka_published_total",
		Help: "Records published, by topic and result (ok, error).",
	}, []string{"topic", "result"})
	consumed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_kafka_consumed_total",
		Help: "Records finished by Consume, by topic and result (ok, dead_lettered).",
	}, []string{"topic", "result"})
	deadLettered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_kafka_dead_lettered_total",
		Help: "Records sent to the dead-letter topic, by source topic and reason (handler_failed, undecodable).",
	}, []string{"topic", "reason"})
	dlqPublishes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_kafka_dlq_publish_total",
		Help: "Attempts to publish a dead letter, by dead-letter topic and result (ok, error); errors are retried until they work.",
	}, []string{"topic", "result"})
	handleDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "admon_kafka_handle_duration_seconds",
		Help:    "Duration of one handler attempt, by topic.",
		Buckets: metrics.DurationBuckets,
	}, []string{"topic"})
	handleRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_kafka_handle_retries_total",
		Help: "Handler attempts repeated after an error, by topic.",
	}, []string{"topic"})
)

// handleObserved runs handle for one record with retries, timing every attempt.
func handleObserved[T any](ctx context.Context, topic string, delay time.Duration, v T, handle func(context.Context, T) error) error {
	attempts := 0
	return retry(ctx, delay, func() error {
		if attempts++; attempts > 1 {
			handleRetries.WithLabelValues(topic).Inc()
		}
		start := time.Now()
		err := handle(ctx, v)
		handleDuration.WithLabelValues(topic).Observe(time.Since(start).Seconds())
		return err
	})
}

// initConsumeSeries creates the consumer-side series at 0 so that increase()
// sees a counter's first increment (a series born at 1 has no earlier sample).
func initConsumeSeries(topic string) {
	for _, r := range []string{"ok", "dead_lettered"} {
		consumed.WithLabelValues(topic, r)
	}
	for _, r := range []string{"handler_failed", "undecodable"} {
		deadLettered.WithLabelValues(topic, r)
	}
	for _, r := range []string{"ok", "error"} {
		dlqPublishes.WithLabelValues(DLQTopic(topic), r)
	}
}
