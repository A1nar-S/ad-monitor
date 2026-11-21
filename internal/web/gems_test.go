package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
)

type fakeGems struct {
	busy  bool
	query gems.Query
}

func (f *fakeGems) RequestScan(_ context.Context, source string) (gems.Scan, error) {
	if f.busy {
		return gems.Scan{}, gems.ErrBusy
	}
	f.busy = true
	return gems.Scan{ID: 1, Source: source, Status: "requested"}, nil
}
func (f *fakeGems) LatestScan(context.Context, string) (gems.Scan, bool, error) {
	return gems.Scan{ID: 1, Status: "running", Pages: 12}, f.busy, nil
}
func (f *fakeGems) List(_ context.Context, _ string, q gems.Query) (gems.Page, error) {
	if q.Buy == "maybe" {
		return gems.Page{}, gems.ErrBadQuery
	}
	f.query = q
	return gems.Page{Items: []gems.Gem{{ExternalID: "1", Buy: "good"}}, Total: 1, Page: 1, PageSize: gems.PageSize}, nil
}

func TestGemEndpoints(t *testing.T) {
	g := &fakeGems{}
	h := New(Config{Searches: &fakeSearches{}, Gems: g, GemSource: "ss.com"})

	for _, path := range []string{"/api/gems", "/api/gems/scan"} {
		if w := do(h, "GET", path, "s-alice", "", false); w.Code != http.StatusForbidden {
			t.Errorf("%s for a member: %d, want 403", path, w.Code)
		}
	}
	if w := do(h, "GET", "/api/gems/scan", "s-admin", "", false); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("no scan yet: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-admin", "", true); w.Code != http.StatusAccepted {
		t.Errorf("request: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-admin", "", true); w.Code != http.StatusConflict {
		t.Errorf("second request: %d, want 409", w.Code)
	}
	if w := do(h, "GET", "/api/gems/scan", "s-admin", "", false); !strings.Contains(w.Body.String(), `"status":"running"`) {
		t.Errorf("scan status: %s", w.Body)
	}

	w := do(h, "GET", "/api/gems?make=BMW&maxPrice=9000&minYear=2015&page=2&sort=price", "s-admin", "", false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"buy":"good"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if q := g.query; q.Make != "BMW" || q.MaxPrice != 9000 || q.MinYear != 2015 || q.Page != 2 || q.Sort != "price" {
		t.Errorf("query = %+v", q)
	}
	for _, bad := range []string{"/api/gems?maxPrice=cheap", "/api/gems?minYear=-1", "/api/gems?buy=maybe"} {
		if w := do(h, "GET", bad, "s-admin", "", false); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	if w := do(h, "GET", "/api/me", "s-admin", "", false); !strings.Contains(w.Body.String(), `"gems":true`) {
		t.Errorf("me for the admin: %s", w.Body)
	}
	if w := do(h, "GET", "/api/me", "s-alice", "", false); !strings.Contains(w.Body.String(), `"gems":false`) {
		t.Errorf("me for a member: %s", w.Body)
	}
}
