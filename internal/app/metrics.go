package app

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

var (
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
	if _, err := reg.Get(source); err != nil {
		source = "unknown"
	}
	result := "unmatched"
	if notifications > 0 {
		result = "matched"
	}
	matcherListings.WithLabelValues(source, result).Inc()
	matcherNotifications.WithLabelValues(source).Add(float64(notifications))
}
