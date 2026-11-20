// Package metrics exposes Prometheus metrics for the services. Each package
// registers its own counters on the default registry (which already carries
// the Go runtime and process collectors); a service calls Serve to publish
// them on METRICS_ADDR.
//
// Label values must stay bounded: source names, topics, kinds, results, route
// patterns and status codes, never URLs, user or search IDs, or error text.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler serves g in the Prometheus text format.
func Handler(g prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{})
}

// Serve publishes the default registry as /metrics on addr until ctx is done.
// An empty addr disables it. A busy port only logs a warning: metrics are not
// worth stopping a service for (several services share a host in development).
func Serve(ctx context.Context, addr string) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Warn("metrics disabled: cannot listen", "addr", addr, "err", err)
		return
	}
	serve(ctx, ln, prometheus.DefaultGatherer)
}

func serve(ctx context.Context, ln net.Listener, g prometheus.Gatherer) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler(g))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("metrics", "listen", ln.Addr().String())
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
}

// SetGaugeFunc registers (or replaces) a gauge read by calling fn at scrape
// time, for values that are cheap to look up on demand.
func SetGaugeFunc(name, help string, fn func() float64) {
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, fn)
	err := prometheus.Register(g)
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) { // a second service started in the same process (tests)
		prometheus.Unregister(are.ExistingCollector)
		err = prometheus.Register(g)
	}
	if err != nil {
		slog.Warn("metrics: cannot register gauge", "name", name, "err", err)
	}
}

// DurationBuckets suit request-sized work: tens of milliseconds to a minute.
var DurationBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

// StatusClass maps an HTTP status code to "2xx", "4xx", ... for a bounded label.
func StatusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	}
	return "other"
}
