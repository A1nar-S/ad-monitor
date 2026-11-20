package subscriptions

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/metrics"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

var (
	// Counted per read attempt: a Kafka retry of the same listing reads again.
	adPageReads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_matcher_adpage_reads_total",
		Help: "Ad page reads (enrichment attempts) by source and result (ok, gone, failed).",
	}, []string{"source", "result"})

	watchChecks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_watch_checks_total",
		Help: "Price watch page checks by result (ok, failed, blocked, gone, no_price).",
	}, []string{"result"})
	watchCheckDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "admon_watch_check_duration_seconds",
		Help:    "Duration of one price watch check: reading the page and recording the result.",
		Buckets: metrics.DurationBuckets,
	})
	watchAlerts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_watch_alerts_total",
		Help: "Price watch alerts queued for delivery by kind (drop, failing, recovered).",
	}, []string{"kind"})
)

// watchResult classifies a page read for the metrics.
func watchResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, sources.ErrPageGone):
		return "gone"
	case errors.Is(err, sources.ErrBlocked):
		return "blocked"
	case errors.Is(err, sources.ErrNoPrice):
		return "no_price"
	}
	return "failed"
}
