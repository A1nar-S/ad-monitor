package metrics

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServeExposesRegistryUntilContextEnds(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "admon_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Add(3)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serve(ctx, ln, reg)

	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "admon_test_total 3") {
		t.Fatalf("metric missing from:\n%s", body)
	}

	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("server still up after ctx was cancelled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServeEmptyAddrIsDisabled(t *testing.T) {
	Serve(context.Background(), "") // must return at once without listening
}

func TestDefaultRegistryHasRuntimeCollectors(t *testing.T) {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var goRuntime, process bool
	for _, mf := range mfs {
		goRuntime = goRuntime || strings.HasPrefix(mf.GetName(), "go_goroutines")
		process = process || strings.HasPrefix(mf.GetName(), "process_")
	}
	if !goRuntime {
		t.Error("no go runtime metrics")
	}
	_ = process // process collector is Linux/Windows only; not asserted
}

func TestStatusClass(t *testing.T) {
	for code, want := range map[int]string{200: "2xx", 204: "2xx", 301: "3xx", 404: "4xx", 429: "4xx", 503: "5xx", 99: "other"} {
		if got := StatusClass(code); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestRoute(t *testing.T) {
	for in, want := range map[string]string{
		"GET /api/searches/{id}": "/api/searches/{id}",
		"/api/":                  "/api/",
		"/":                      "/",
		"":                       "unmatched",
	} {
		if got := Route(in); got != want {
			t.Errorf("Route(%q) = %q, want %q", in, got, want)
		}
	}
}

// The route label must be the pattern, so a path with an ID in it never
// creates a new series.
func TestHTTPUsesRoutePatternNotPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/searches/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mux.HandleFunc("GET /ok", func(http.ResponseWriter, *http.Request) {})
	h := HTTP(mux)

	do := func(method, path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	}
	teapot := httpRequests.WithLabelValues("GET", "/api/searches/{id}", "418")
	ok := httpRequests.WithLabelValues("GET", "/ok", "200")
	missing := httpRequests.WithLabelValues("GET", "unmatched", "404")
	weird := httpRequests.WithLabelValues("other", "unmatched", "405")
	before := [4]float64{testutil.ToFloat64(teapot), testutil.ToFloat64(ok), testutil.ToFloat64(missing), testutil.ToFloat64(weird)}

	for _, id := range []string{"1", "22", "333"} {
		do("GET", "/api/searches/"+id)
	}
	do("GET", "/ok")
	do("GET", "/nothing/at/this/path/42")
	do("BREW", "/ok")

	after := [4]float64{testutil.ToFloat64(teapot), testutil.ToFloat64(ok), testutil.ToFloat64(missing), testutil.ToFloat64(weird)}
	want := [4]float64{3, 1, 1, 1}
	for i := range want {
		if after[i]-before[i] != want[i] {
			t.Errorf("counter %d rose by %v, want %v", i, after[i]-before[i], want[i])
		}
	}
}
