package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Gems is the gem finder: every car for sale on a source, rated. Everyone
// may browse it; only admins start a scan.
type Gems interface {
	RequestScan(ctx context.Context, source string) (gems.Scan, error)
	LatestScan(ctx context.Context, source string) (gems.Scan, bool, error)
	List(ctx context.Context, source string, q gems.Query) (gems.Page, error)
}

func (s *server) routeGems(mux *http.ServeMux) {
	if s.Gems == nil || s.GemSource == "" {
		return
	}
	mux.Handle("GET /api/gems/sources", s.auth(s.gemSources))
	mux.Handle("GET /api/gems", s.auth(s.listGems))
	mux.Handle("GET /api/gems/scan", s.auth(s.gemScan))
	mux.Handle("POST /api/gems/scan", s.auth(s.admin(s.requestGemScan)))
}

// gemSource is a site the finder covers and what it can be scanned for.
type gemSource struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Categories []string `json:"categories"`
}

func (s *server) gemSources(w http.ResponseWriter, _ *http.Request, _ subscriptions.Owner) {
	writeJSON(w, http.StatusOK, []gemSource{{ID: s.GemSource, Name: s.GemSource, Categories: gems.Categories}})
}

// gemTarget checks ?source= and ?category= (both optional: the one there
// is); false after answering 400.
func (s *server) gemTarget(w http.ResponseWriter, v url.Values) bool {
	if src := v.Get("source"); src != "" && src != s.GemSource {
		writeError(w, http.StatusBadRequest, "unknown source "+strconv.Quote(src))
		return false
	}
	if c := v.Get("category"); c != "" && !slices.Contains(gems.Categories, c) {
		writeError(w, http.StatusBadRequest, "unknown category "+strconv.Quote(c))
		return false
	}
	return true
}

// gemScan reports the latest scan (null before the first). Why one failed
// or stopped early is for admins: it can quote the database.
func (s *server) gemScan(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	if !s.gemTarget(w, r.URL.Query()) {
		return
	}
	scan, ok, err := s.Gems.LatestScan(r.Context(), s.GemSource)
	if s.fail(w, err) {
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	if !o.Admin {
		scan.Error, scan.Note = "", ""
	}
	writeJSON(w, http.StatusOK, scan)
}

func (s *server) requestGemScan(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	if !s.gemTarget(w, r.URL.Query()) {
		return
	}
	scan, err := s.Gems.RequestScan(r.Context(), s.GemSource)
	if errors.Is(err, gems.ErrBusy) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, scan)
}

// maxGemParam bounds the numbers a list takes: any real price, year, km or
// page is below it, and it keeps the database's int32 offsets in range.
const maxGemParam = 1e7

// listGems: ?source=&category=&make=&model=&buy=&fuel=&gearbox=&body=&region=
// (each list one value per param, repeated) &minPrice=&maxPrice=&minYear=
// &maxYear=&maxMileage=&minEngine=&maxEngine=&detailed=1&inspected=1&sort=&page=
func (s *server) listGems(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	v := r.URL.Query()
	if !s.gemTarget(w, v) {
		return
	}
	bad := ""
	num := func(key string) float64 {
		if v.Get(key) == "" || bad != "" {
			return 0
		}
		n, err := strconv.ParseFloat(v.Get(key), 64)
		if err != nil || n < 0 || n > maxGemParam {
			bad = key + " must be a positive number"
		}
		return n
	}
	q := gems.Query{
		Make: v.Get("make"), Model: v.Get("model"), Buy: v.Get("buy"), Sort: v.Get("sort"),
		Fuels: v["fuel"], Gearboxes: v["gearbox"], Bodies: v["body"], Regions: v["region"],
		MinPrice: num("minPrice"), MaxPrice: num("maxPrice"),
		MinYear: int(num("minYear")), MaxYear: int(num("maxYear")), MaxMileage: int(num("maxMileage")),
		MinEngineL: num("minEngine"), MaxEngineL: num("maxEngine"),
		DetailedOnly:  v.Get("detailed") == "1" || v.Get("detailed") == "true",
		InspectedOnly: v.Get("inspected") == "1" || v.Get("inspected") == "true",
		Page:          int(num("page")),
	}
	if bad != "" {
		writeError(w, http.StatusBadRequest, bad)
		return
	}
	out, err := s.Gems.List(r.Context(), s.GemSource, q)
	if errors.Is(err, gems.ErrBadQuery) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}
