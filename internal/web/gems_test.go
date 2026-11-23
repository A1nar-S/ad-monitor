package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/cars"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

type fakeGems struct {
	busy     bool
	category string
	mode     gems.Mode
	query    gems.Query
}

func (f *fakeGems) CheckRequest(context.Context, string, string, gems.Mode) error {
	if f.busy {
		return gems.ErrBusy
	}
	return nil
}

// request stands in for the scraper recording a requested scan.
func (f *fakeGems) request(_ context.Context, r events.GemScanRequested) error {
	f.busy, f.mode = true, gems.Mode(r.Mode)
	return nil
}
func (f *fakeGems) LatestScan(context.Context, string, string) (gems.Scan, bool, error) {
	return gems.Scan{ID: 1, Status: "running", Pages: 12, Error: "pq: relation gems", Note: "stopped"}, f.busy, nil
}
func (f *fakeGems) List(_ context.Context, _, category string, q gems.Query) (gems.Page, error) {
	if q.Buy == "maybe" {
		return gems.Page{}, gems.ErrBadQuery
	}
	f.category, f.query = category, q
	return gems.Page{Items: []gems.Gem{{ExternalID: "1", Buy: "good"}}, Total: 1, Page: 1, PageSize: gems.PageSize}, nil
}
func (*fakeGems) Category(id string) (gems.Category, bool) {
	if id == "cars" {
		return cars.Category{}, true
	}
	return nil, false
}

// catalogSite is a source with a search catalog, for the region options.
type catalogSite struct{ *fakeSite }

func (catalogSite) Name() string { return "ss.com" }
func (catalogSite) Categories() []sources.Category {
	return []sources.Category{{ID: "cars", Fields: []sources.FieldSpec{{Key: "region", Options: []string{"Rīga", "Jūrmala"}}}}}
}
func (catalogSite) CompileSearch(string, map[string]any) (sources.Compiled, error) {
	return sources.Compiled{}, nil
}

func TestGemEndpoints(t *testing.T) {
	g := &fakeGems{}
	h := New(Config{Searches: &fakeSearches{}, Gems: g, RequestGemScan: g.request, Sources: sources.NewRegistry(catalogSite{&fakeSite{}}),
		GemSources: []GemSource{{ID: "ss.com", Name: "ss.com", Categories: []string{"cars"}}}})

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
	for _, path := range []string{"/api/gems", "/api/gems/scan", "/api/gems/sources"} {
		if w := do(h, "GET", path, "", "", false); w.Code != http.StatusOK {
			t.Errorf("%s for a guest: %d %s", path, w.Code, w.Body)
		}
	}
	if w := do(h, "POST", "/api/gems/scan", "", "", true); w.Code != http.StatusUnauthorized {
		t.Errorf("scan by a guest: %d, want 401", w.Code)
	}
	if w := do(h, "GET", "/api/guest", "", "", false); strings.TrimSpace(w.Body.String()) != `{"gems":true}` {
		t.Errorf("guest: %s", w.Body)
	}
	w := do(h, "GET", "/api/gems/sources", "s-alice", "", false)
	if body := w.Body.String(); !strings.Contains(body, `"id":"cars"`) || !strings.Contains(body, `"key":"body"`) ||
		!strings.Contains(body, `"options":["Rīga","Jūrmala"]`) {
		t.Errorf("sources, with the cars spec and the catalog's regions: %d %s", w.Code, body)
	}
	if w := do(h, "POST", "/api/gems/scan?mode=bogus", "s-admin", "", true); w.Code != http.StatusBadRequest {
		t.Errorf("unknown mode: %d, want 400", w.Code)
	}
	if w := do(h, "POST", "/api/gems/scan", "s-admin", "", true); w.Code != http.StatusAccepted || g.mode != gems.Full {
		t.Errorf("request: %d %s, mode %q; want a full scan", w.Code, w.Body, g.mode)
	}
	g.busy = false
	if w := do(h, "POST", "/api/gems/scan?mode=rate", "s-admin", "", true); w.Code != http.StatusAccepted || g.mode != gems.Rate {
		t.Errorf("recalculation: %d %s, mode %q", w.Code, w.Body, g.mode)
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

	w = do(h, "GET", "/api/gems?source=ss.com&category=cars&make=BMW&maxPrice=9000&year_min=2015&year_max=2019&page=2&sort=year"+
		"&body=wagon&body=sedan&region=R%C4%ABga&fuel=diesel&gearbox=automatic&engine_l_min=1.6&detailed=1&inspection=valid&lang=lv&nonsense=1", "s-alice", "", false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"buy":"good"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	q := g.query
	if g.category != "cars" || q.MaxPrice != 9000 || q.Page != 2 || q.Sort != "year" || !q.DetailedOnly ||
		q.Ranges["year"] != [2]float64{2015, 2019} || q.Ranges["engine_l"] != [2]float64{1.6, 0} ||
		strings.Join(q.Choices["body"], ",") != "wagon,sedan" || strings.Join(q.Choices["region"], ",") != "Rīga" ||
		q.Choices["make"][0] != "BMW" || q.Choices["fuel"][0] != "diesel" || q.Choices["gearbox"][0] != "automatic" ||
		q.Choices["inspection"][0] != "valid" || len(q.Choices) != 6 || len(q.Ranges) != 2 || q.Lang != "lv" {
		t.Errorf("query = %+v", q)
	}
	if w := do(h, "GET", "/api/gems?lang=Latvian", "s-alice", "", false); w.Code != http.StatusOK || g.category != "cars" || g.query.Lang != "" {
		t.Errorf("no source or category: the first one; %d %s; a language not a code is ignored: %q", w.Code, g.category, g.query.Lang)
	}
	for _, bad := range []string{"/api/gems?maxPrice=cheap", "/api/gems?year_min=-1", "/api/gems?buy=maybe",
		"/api/gems?source=other", "/api/gems?category=flats", "/api/gems?page=1e12", "/api/gems?year_min=x"} {
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
