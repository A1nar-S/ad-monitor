package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_http_requests_total",
		Help: "HTTP requests served, by method, route pattern and status code.",
	}, []string{"method", "route", "code"})
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "admon_http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: DurationBuckets,
	}, []string{"method", "route"})
)

// HTTP records every request that passes through next. The route label is the
// ServeMux pattern that matched ("/api/searches/{id}"), never the raw path, so
// the label set stays small; requests no pattern matched are "unmatched".
// next must be (or end in) a *http.ServeMux, which sets Request.Pattern.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		method, route := methodLabel(r.Method), Route(r.Pattern)
		httpRequests.WithLabelValues(method, route, strconv.Itoa(rec.code)).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}

// Route turns a ServeMux pattern ("GET /api/searches/{id}") into the route
// label ("/api/searches/{id}").
func Route(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		return strings.TrimSpace(rest)
	}
	return pattern
}

// methodLabel keeps arbitrary client-sent methods out of the label set.
func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions:
		return m
	}
	return "other"
}

type statusRecorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.code, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
