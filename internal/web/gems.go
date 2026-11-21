package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Gems is the admin's gem finder: scan every car for sale, list them rated.
type Gems interface {
	RequestScan(ctx context.Context, source string) (gems.Scan, error)
	LatestScan(ctx context.Context, source string) (gems.Scan, bool, error)
	List(ctx context.Context, source string, q gems.Query) (gems.Page, error)
}

func (s *server) routeGems(mux *http.ServeMux) {
	if s.Gems == nil || s.GemSource == "" {
		return
	}
	mux.Handle("GET /api/gems", s.auth(s.admin(s.listGems)))
	mux.Handle("GET /api/gems/scan", s.auth(s.admin(s.gemScan)))
	mux.Handle("POST /api/gems/scan", s.auth(s.admin(s.requestGemScan)))
}

// gemScan reports the latest scan (null before the first).
func (s *server) gemScan(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	scan, ok, err := s.Gems.LatestScan(r.Context(), s.GemSource)
	if s.fail(w, err) {
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, scan)
}

func (s *server) requestGemScan(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
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

// listGems: ?make=&model=&buy=&maxPrice=&minYear=&maxMileage=&sort=&page=
func (s *server) listGems(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	v := r.URL.Query()
	num := func(key string) (float64, bool) {
		if v.Get(key) == "" {
			return 0, true
		}
		n, err := strconv.ParseFloat(v.Get(key), 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, key+" must be a positive number")
			return 0, false
		}
		return n, true
	}
	maxPrice, ok1 := num("maxPrice")
	minYear, ok2 := num("minYear")
	maxMileage, ok3 := num("maxMileage")
	page, ok4 := num("page")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	out, err := s.Gems.List(r.Context(), s.GemSource, gems.Query{
		Make: v.Get("make"), Model: v.Get("model"), Buy: v.Get("buy"), Sort: v.Get("sort"),
		MaxPrice: maxPrice, MinYear: int(minYear), MaxMileage: int(maxMileage), Page: int(page),
	})
	if errors.Is(err, gems.ErrBadQuery) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}
