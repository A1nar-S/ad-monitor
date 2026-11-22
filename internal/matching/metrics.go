package matching

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

var (
	// Counted per read attempt: a Kafka retry of the same listing reads again.
	adPageReads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_matcher_adpage_reads_total",
		Help: "Ad page reads (enrichment attempts) by source and result (ok, gone, failed).",
	}, []string{"source", "result"})
	matcherListings = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_matcher_listings_total",
		Help: "New listings run through the searches, by source and result (matched: at least one notification, unmatched).",
	}, []string{"source", "result"})
	matcherNotifications = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_matcher_notifications_total",
		Help: "Notification requests the matcher produced and recorded, by source.",
	}, []string{"source"})
)

// observeMatch counts one fully handled listing. A source nobody registered
// is "unknown", so a bad record cannot create series.
func observeMatch(reg sources.Registry, source string, notifications int) {
	if reg.Lookup(source) == nil {
		source = "unknown"
	}
	result := "unmatched"
	if notifications > 0 {
		result = "matched"
	}
	matcherListings.WithLabelValues(source, result).Inc()
	matcherNotifications.WithLabelValues(source).Add(float64(notifications))
}
