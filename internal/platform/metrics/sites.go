package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// siteRequests counts every HTTP request a source adapter makes to its site
// (feed pages, ad pages, API calls), by status class; "error" is a request
// that got no response. It lives here, not in an adapter, because every
// adapter reports to the same metric and promauto may register it only once.
var siteRequests = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "admon_scraper_requests_total",
	Help: "HTTP requests to a source site by source and status class (2xx, 4xx, ..., error).",
}, []string{"source", "status_class"})

// SiteRequest records one response from a source's site.
func SiteRequest(source string, code int) {
	siteRequests.WithLabelValues(source, StatusClass(code)).Inc()
}

// SiteRequestFailed records a request to a source's site that got no response.
func SiteRequestFailed(source string) {
	siteRequests.WithLabelValues(source, "error").Inc()
}
