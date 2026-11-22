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
	return gems.Scan{ID: 1, Status: "running", Pages: 12, Error: "pq: relation gems", Note: "stopped"}, f.busy, nil
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

	// Everyone browses; only admins start a scan.
	if w := do(h, "GET", "/api/gems/scan", "s-alice", "", false); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("no scan yet, for a member: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/gems", "s-alice", "", false); w.Code != http.StatusOK {
		t.Errorf("list for a member: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-alice", "", true); w.Code != http.StatusForbidden {
		t.Errorf("scan by a member: %d, want 403", w.Code)
	}
	if w := do(h, "GET", "/api/gems", "", "", false); w.Code != http.StatusUnauthorized {
		t.Errorf("list signed out: %d, want 401", w.Code)
	}
	if w := do(h, "GET", "/api/gems/sources", "s-alice", "", false); !strings.Contains(w.Body.String(), `"categories":["cars"]`) {
		t.Errorf("sources: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-admin", "", true); w.Code != http.StatusAccepted {
		t.Errorf("request: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-admin", "", true); w.Code != http.StatusConflict {
		t.Errorf("second request: %d, want 409", w.Code)
	}
	if w := do(h, "GET", "/api/gems/scan", "s-alice", "", false); !strings.Contains(w.Body.String(), `"status":"running"`) ||
		strings.Contains(w.Body.String(), "pq:") || strings.Contains(w.Body.String(), "stopped") {
		t.Errorf("scan status for a member, without the error and note: %s", w.Body)
	}
	if w := do(h, "GET", "/api/gems/scan", "s-admin", "", false); !strings.Contains(w.Body.String(), "pq:") {
		t.Errorf("scan status for the admin keeps the error: %s", w.Body)
	}

	w := do(h, "GET", "/api/gems?source=ss.com&category=cars&make=BMW&maxPrice=9000&minYear=2015&maxYear=2019&page=2&sort=price"+
		"&body=wagon&body=sedan&region=R%C4%ABga&fuel=diesel&gearbox=automatic&minEngine=1.6&detailed=1&inspected=1", "s-alice", "", false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"buy":"good"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	q := g.query
	if q.Make != "BMW" || q.MaxPrice != 9000 || q.MinYear != 2015 || q.MaxYear != 2019 || q.Page != 2 || q.Sort != "price" ||
		strings.Join(q.Bodies, ",") != "wagon,sedan" || strings.Join(q.Regions, ",") != "Rīga" || q.Fuels[0] != "diesel" ||
		q.Gearboxes[0] != "automatic" || q.MinEngineL != 1.6 || !q.DetailedOnly || !q.InspectedOnly {
		t.Errorf("query = %+v", q)
	}
	for _, bad := range []string{"/api/gems?maxPrice=cheap", "/api/gems?minYear=-1", "/api/gems?buy=maybe",
		"/api/gems?source=other", "/api/gems?category=flats", "/api/gems?page=1e12"} {
		if w := do(h, "GET", bad, "s-alice", "", false); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	for _, who := range []string{"s-admin", "s-alice"} {
		if w := do(h, "GET", "/api/me", who, "", false); !strings.Contains(w.Body.String(), `"gems":true`) {
			t.Errorf("me for %s: %s", who, w.Body)
		}
	}
}
