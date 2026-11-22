package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// testSections are where ss.com keeps its cars and flats.
var (
	testSections = []Section{
		{source: "ss.com", category: "cars", location: "transport/cars", adPage: true},
		{source: "ss.com", category: "flats", location: "real-estate/flats", adPage: true},
		{source: "rrr.lt", category: "parts"},
	}
	testCategories = []string{"cars", "flats", "parts"}
)

func TestOverview(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		target string
		err    error
		status int
	}{
		{name: "overview", method: http.MethodGet, target: "/api/overview", status: http.StatusOK},
		{name: "filtered page", method: http.MethodGet, target: "/api/overview?view=listings&q=audi&category=cars&status=active&sort=title&page=3", status: http.StatusOK},
		{name: "bad filter", method: http.MethodGet, target: "/api/overview?sort=price", status: http.StatusBadRequest},
		{name: "database unavailable", method: http.MethodGet, target: "/api/overview", err: errors.New("secret database connection string"), status: http.StatusServiceUnavailable},
		{name: "read only", method: http.MethodPost, target: "/api/overview", status: http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(func(ctx context.Context, _ Query) (json.RawMessage, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("database request must have a deadline")
				}
				return json.RawMessage(`{"items":[]}`), test.err
			}, testCategories)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.status == http.StatusOK {
				if !json.Valid(response.Body.Bytes()) || response.Header().Get("Content-Type") != "application/json" {
					t.Fatal("expected JSON response")
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Error("dashboard data must not be cached")
				}
			}
			if test.err != nil && response.Body.String() != "Dashboard data is unavailable. Check the database and migrations.\n" {
				t.Error("unexpected error response; internal errors must not be exposed")
			}
		})
	}
}

func TestParseQuery(t *testing.T) {
	q, err := ParseQuery(url.Values{}, testCategories)
	if err != nil || q != (Query{View: "listings", Sort: "newest", Page: 1}) {
		t.Fatalf("defaults = %+v, %v", q, err)
	}
	q, err = ParseQuery(url.Values{"view": {"searches"}, "q": {"  alice "}, "status": {"inactive"}, "page": {"4"}}, testCategories)
	if err != nil || q.View != "searches" || q.Text != "alice" || q.Status != "inactive" || q.Page != 4 {
		t.Fatalf("query = %+v, %v", q, err)
	}
	for _, bad := range []url.Values{
		{"view": {"users"}},
		{"category": {"boats"}},
		{"status": {"sold"}},
		{"sort": {"price"}},
		{"page": {"0"}},
		{"page": {"two"}},
	} {
		if _, err := ParseQuery(bad, testCategories); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
}

func TestWhereKeepsInputOutOfSQL(t *testing.T) {
	where, args := newListingsView(testSections).where(Query{Text: "50%_off'; DROP TABLE listings", Source: "evil.example", Status: "inactive", Category: "cars"})
	if strings.Contains(where, "DROP") || strings.Contains(where, "evil.example") {
		t.Fatalf("user input in SQL: %s", where)
	}
	if len(args) != 4 {
		t.Fatalf("args = %v", args)
	}
	if args[0] != `%50\%\_off'; DROP TABLE listings%` {
		t.Errorf("pattern = %q, want LIKE wildcards escaped", args[0])
	}
	if args[2] != false {
		t.Errorf("status arg = %v, want false for inactive", args[2])
	}
	// Searches have no category.
	if _, args := searchesView.where(Query{Category: "cars"}); len(args) != 0 {
		t.Errorf("searches took a category: %v", args)
	}
}
