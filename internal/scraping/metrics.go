package scraping

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	feedFetches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_scraper_feed_fetches_total",
		Help: "Feed page fetches by source and result (ok, empty: page 1 with no rows, blocked, failed).",
	}, []string{"source", "result"})
	listingsDiscovered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_scraper_listings_discovered_total",
		Help: "Listings read from feed pages and published, by source.",
	}, []string{"source"})
	cycles = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_scraper_cycles_total",
		Help: "Scrape cycles by source and result (ok, idle, failed).",
	}, []string{"source", "result"})
	cycleDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "admon_scraper_cycle_duration_seconds",
		Help:    "Duration of a scrape cycle over all active feeds, by source.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
	}, []string{"source"})
	lastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "admon_scraper_last_success_timestamp_seconds",
		Help: "Unix time the last scrape cycle without errors finished, by source.",
	}, []string{"source"})
)

// fetchResult classifies a feed fetch for the metrics.
// An empty page is only suspicious as page 1; deeper pages run out normally.
func fetchResult(page, count int, err error) string {
	var se statusError
	switch {
	case errors.As(err, &se) && blocked(se.HTTPStatus()):
		return "blocked"
	case err != nil:
		return "failed"
	case page == 1 && count == 0:
		return "empty"
	}
	return "ok"
}

func observeCycle(source string, start time.Time, idle bool, err error) {
	cycleDuration.WithLabelValues(source).Observe(time.Since(start).Seconds())
	switch {
	case err != nil:
		cycles.WithLabelValues(source, "failed").Inc()
	case idle:
		cycles.WithLabelValues(source, "idle").Inc()
		lastSuccess.WithLabelValues(source).SetToCurrentTime()
	default:
		cycles.WithLabelValues(source, "ok").Inc()
		lastSuccess.WithLabelValues(source).SetToCurrentTime()
	}
}
