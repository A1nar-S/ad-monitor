package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Gems is the gem finder: every listing of a category for sale on a source,
// rated. Everyone may browse it; only admins start a scan.
type Gems interface {
	RequestScan(ctx context.Context, source, category string, mode gems.Mode) (gems.Scan, error)
	LatestScan(ctx context.Context, source, category string) (gems.Scan, bool, error)
	List(ctx context.Context, source, category string, q gems.Query) (gems.Page, error)
	Category(id string) (gems.Category, bool)
}

// GemSource is a site the finder covers and the categories it can be
// scanned for.
type GemSource struct {
	ID         string
	Name       string
	Categories []string
}

func (s *server) routeGems(mux *http.ServeMux) {
	if s.Gems == nil || len(s.GemSources) == 0 {
		return
	}
	mux.Handle("GET /api/gems/sources", s.auth(s.gemSources))
	mux.Handle("GET /api/gems", s.auth(s.listGems))
	mux.Handle("GET /api/gems/scan", s.auth(s.gemScan))
	mux.Handle("POST /api/gems/scan", s.auth(s.admin(s.requestGemScan)))
}

type gemCategoryJSON struct {
	ID   string    `json:"id"`
	Spec gems.Spec `json:"spec"`
}

type gemSourceJSON struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Categories []gemCategoryJSON `json:"categories"`
}

// gemSources lists the sites, each with its categories and what can be
// filtered on in each.
func (s *server) gemSources(w http.ResponseWriter, _ *http.Request, _ subscriptions.Owner) {
	out := []gemSourceJSON{}
	for _, src := range s.GemSources {
		j := gemSourceJSON{ID: src.ID, Name: src.Name, Categories: []gemCategoryJSON{}}
		for _, id := range src.Categories {
			if c, ok := s.Gems.Category(id); ok {
				j.Categories = append(j.Categories, gemCategoryJSON{ID: id, Spec: s.withCatalog(src.ID, id, c.Spec())})
			}
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

// withCatalog fills the Catalog fields' options from the source's search
// catalog: the same names the Searches tab offers (ss.com: its codebook).
func (s *server) withCatalog(source, category string, spec gems.Spec) gems.Spec {
	catalog, ok := s.Sources[source].(sources.Catalog)
	if !ok {
		return spec
	}
	var fields []sources.FieldSpec
	for _, c := range catalog.Categories() {
		if c.ID == category {
			fields = c.Fields
		}
	}
	spec.Fields = slices.Clone(spec.Fields)
	for i, f := range spec.Fields {
		if !f.Catalog {
			continue
		}
		if j := slices.IndexFunc(fields, func(x sources.FieldSpec) bool { return x.Key == f.Key }); j >= 0 {
			spec.Fields[i].Options = fields[j].Options
		}
	}
	return spec
}

// gemTarget reads ?source= and ?category=; each defaults to the first there
// is. ok is false after answering 400.
func (s *server) gemTarget(w http.ResponseWriter, v url.Values) (source string, cat gems.Category, ok bool) {
	i := 0
	if src := v.Get("source"); src != "" {
		i = slices.IndexFunc(s.GemSources, func(g GemSource) bool { return g.ID == src })
		if i < 0 {
			writeError(w, http.StatusBadRequest, "unknown source "+strconv.Quote(src))
			return "", nil, false
		}
	}
	src := s.GemSources[i]
	id := v.Get("category")
	if id == "" {
		id = src.Categories[0]
	}
	c, found := s.Gems.Category(id)
	if !found || !slices.Contains(src.Categories, id) {
		writeError(w, http.StatusBadRequest, "unknown category "+strconv.Quote(id))
		return "", nil, false
	}
	return src.ID, c, true
}

// gemScan reports the latest scan (null before the first). Why one failed
// or stopped early is for admins: it can quote the database.
func (s *server) gemScan(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	source, cat, ok := s.gemTarget(w, r.URL.Query())
	if !ok {
		return
	}
	scan, ok, err := s.Gems.LatestScan(r.Context(), source, cat.ID())
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

// requestGemScan: ?mode=full (the default: every list page read again) or
// rate (the stored listings rated again).
func (s *server) requestGemScan(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	v := r.URL.Query()
	source, cat, ok := s.gemTarget(w, v)
	if !ok {
		return
	}
	mode := gems.Full
	if m := v.Get("mode"); m != "" {
		mode = gems.Mode(m)
	}
	if !slices.Contains(gems.Modes, mode) {
		writeError(w, http.StatusBadRequest, "unknown mode "+strconv.Quote(string(mode)))
		return
	}
	scan, err := s.Gems.RequestScan(r.Context(), source, cat.ID(), mode)
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

// listGems: ?source=&category=&buy=&sort=&page=&minPrice=&maxPrice=&detailed=1,
// and the category's fields: a choice as <key>=value (repeated: any of
// them), a range as <key>_min= and <key>_max=.
func (s *server) listGems(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	v := r.URL.Query()
	source, cat, ok := s.gemTarget(w, v)
	if !ok {
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
		Buy: v.Get("buy"), Sort: v.Get("sort"),
		MinPrice: num("minPrice"), MaxPrice: num("maxPrice"), Page: int(num("page")),
		DetailedOnly: v.Get("detailed") == "1" || v.Get("detailed") == "true",
		Choices:      map[string][]string{}, Ranges: map[string][2]float64{},
	}
	for _, f := range cat.Spec().Fields {
		switch f.Kind {
		case gems.Choice:
			if vs := v[f.Key]; len(vs) > 0 {
				q.Choices[f.Key] = vs
			}
		case gems.Range:
			if lo, hi := num(f.Key+"_min"), num(f.Key+"_max"); lo > 0 || hi > 0 {
				q.Ranges[f.Key] = [2]float64{lo, hi}
			}
		}
	}
	if bad != "" {
		writeError(w, http.StatusBadRequest, bad)
		return
	}
	out, err := s.Gems.List(r.Context(), source, cat.ID(), q)
	if errors.Is(err, gems.ErrBadQuery) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}
