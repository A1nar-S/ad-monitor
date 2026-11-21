package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/pages"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Watches is what the dashboard needs for price watches.
type Watches interface {
	ListWatches(ctx context.Context, o subscriptions.Owner, all bool) ([]subscriptions.WatchView, error)
	GetWatch(ctx context.Context, o subscriptions.Owner, id int64) (subscriptions.WatchView, error)
	CreateWatch(ctx context.Context, o subscriptions.Owner, in subscriptions.WatchInput) (subscriptions.WatchView, error)
	UpdateWatch(ctx context.Context, o subscriptions.Owner, id int64, in subscriptions.WatchInput) (subscriptions.WatchView, error)
	SetWatchEnabled(ctx context.Context, o subscriptions.Owner, id int64, enabled bool) error
	DeleteWatch(ctx context.Context, o subscriptions.Owner, id int64) error
	ResetWatchReference(ctx context.Context, o subscriptions.Owner, id int64) (subscriptions.WatchView, error)
	CheckWatchNow(ctx context.Context, o subscriptions.Owner, id int64, r sources.PageReader) (subscriptions.WatchView, error)
	WatchHistory(ctx context.Context, o subscriptions.Owner, id int64, limit int) ([]subscriptions.PricePoint, []subscriptions.WatchAlert, error)
	AllowedWatchIntervals() []int
}

// Pages fetches pages for the element picker and reads prices.
type Pages interface {
	Fetch(ctx context.Context, url string) (*pages.Page, error)
	ReadPrice(ctx context.Context, url string, x sources.Extractor) (sources.Reading, error)
}

const (
	// snapshotTTL is how long a picked-on copy of a page is kept: long
	// enough to pick and fill in the form at leisure.
	snapshotTTL  = 30 * time.Minute
	maxSnapshots = 40
	// inspectGap spaces one user's page loads, which the server makes on
	// their behalf.
	inspectGap = 2 * time.Second
	// maxJSONTree is the largest JSON response the picker shows as a tree.
	maxJSONTree = 512 << 10
)

type watchState struct {
	snapshots *pages.Snapshots
	mu        sync.Mutex
	lastLoad  map[int64]time.Time
}

func (s *server) routeWatches(mux *http.ServeMux) {
	if s.Watches == nil || s.Pages == nil {
		return
	}
	s.watch = &watchState{snapshots: pages.NewSnapshots(snapshotTTL, maxSnapshots), lastLoad: map[int64]time.Time{}}
	mux.Handle("POST /api/pages/inspect", s.auth(s.inspectPage))
	mux.HandleFunc("GET /api/pages/snapshots/{id}", s.snapshotPage) // no session: the id is the secret
	mux.Handle("POST /api/pages/test", s.auth(s.testExtractor))
	mux.Handle("GET /api/watches", s.auth(s.listWatches))
	mux.Handle("POST /api/watches", s.auth(s.createWatch))
	mux.Handle("GET /api/watches/{id}", s.auth(s.getWatch))
	mux.Handle("PUT /api/watches/{id}", s.auth(s.updateWatch))
	mux.Handle("PATCH /api/watches/{id}", s.auth(s.patchWatch))
	mux.Handle("DELETE /api/watches/{id}", s.auth(s.deleteWatch))
	mux.Handle("POST /api/watches/{id}/reset", s.auth(s.resetWatch))
	mux.Handle("POST /api/watches/{id}/check", s.auth(s.checkWatch))
}

// --- the element picker ---------------------------------------------------------

type inspectView struct {
	Snapshot    string             `json:"snapshot"`
	URL         string             `json:"url"` // after redirects
	Kind        string             `json:"kind"`
	Title       string             `json:"title"`
	ImageURL    string             `json:"imageUrl"`
	Currency    string             `json:"currency"` // best guess for values without one
	JSON        any                `json:"json,omitempty"`
	JSONTooBig  bool               `json:"jsonTooBig,omitempty"`
	Suggestions []pages.Suggestion `json:"suggestions"`
	Fetched     time.Time          `json:"fetched"`
}

// inspectPage loads a page for the picker: a copy to point at, plus the
// prices found without pointing.
func (s *server) inspectPage(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	var b struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !s.allowLoad(o.UserID) {
		writeError(w, http.StatusTooManyRequests, "Wait a moment before loading another page.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	p, err := s.Pages.Fetch(ctx, b.URL)
	if err != nil {
		s.pageError(w, err)
		return
	}
	title, image := pages.About(p)
	v := inspectView{
		Snapshot: s.watch.snapshots.Put(o.UserID, p), URL: p.URL, Kind: "html", Title: title, ImageURL: image,
		Currency: pages.GuessCurrency(p), Suggestions: pages.Suggest(p), Fetched: p.FetchedAt,
	}
	if v.Suggestions == nil {
		v.Suggestions = []pages.Suggestion{}
	}
	if p.IsJSON() {
		v.Kind = "json"
		if len(p.Body) > maxJSONTree {
			v.JSONTooBig = true
		} else if err := json.Unmarshal(p.Body, &v.JSON); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "The address answered with broken JSON.")
			return
		}
		if v.Currency == "" && len(v.Suggestions) > 0 {
			v.Currency = v.Suggestions[0].Price.Currency
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) allowLoad(userID int64) bool {
	s.watch.mu.Lock()
	defer s.watch.mu.Unlock()
	if time.Since(s.watch.lastLoad[userID]) < inspectGap {
		return false
	}
	s.watch.lastLoad[userID] = time.Now()
	return true
}

// snapshotPage serves the script-free copy of an inspected page, with the
// picker, for the dashboard's sandboxed iframe.
func (s *server) snapshotPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.watch.snapshots.Get(r.PathValue("id"))
	if !ok || p.IsJSON() {
		http.Error(w, "This page copy has expired. Load the page again.", http.StatusGone)
		return
	}
	nonce := pages.Nonce()
	out, err := pages.PickerHTML(p, nonce)
	if err != nil {
		slog.Warn("picker copy", "url", p.URL, "err", err)
		http.Error(w, "Couldn't prepare the page.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", pages.PickerCSP(nonce))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

type testView struct {
	Raw     string         `json:"raw"`
	Price   *sources.Price `json:"price"`
	Matches int            `json:"matches"`
	Error   string         `json:"error,omitempty"`
}

// testExtractor reads a price from an inspected copy the way the watch
// will read the live page later: the picker's "the server reads: …".
func (s *server) testExtractor(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	var b struct {
		Snapshot  string            `json:"snapshot"`
		Extractor sources.Extractor `json:"extractor"`
	}
	if !decode(w, r, &b) {
		return
	}
	p, ok := s.ownSnapshot(o, b.Snapshot)
	if !ok {
		writeError(w, http.StatusGone, "This page copy has expired. Load the page again.")
		return
	}
	res, err := pages.Extract(p, b.Extractor)
	v := testView{Raw: res.Raw, Matches: res.Matches}
	if err != nil {
		v.Error = err.Error()
		var inv *sources.InvalidSearch
		if errors.As(err, &inv) {
			v.Error = inv.Msg
		}
	} else {
		v.Price = &res.Price
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) ownSnapshot(o subscriptions.Owner, id string) (*pages.Page, bool) {
	p, owner, ok := s.watch.snapshots.Get(id)
	return p, ok && owner == o.UserID
}

// pageError explains why a page could not be loaded.
func (s *server) pageError(w http.ResponseWriter, err error) {
	var inv *sources.InvalidSearch
	var st *pages.StatusError
	code := ""
	if errors.As(err, &st) {
		code = " (" + strconv.Itoa(st.Code) + ")"
	}
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusUnprocessableEntity, capitalize(inv.Msg)+".")
	case errors.Is(err, sources.ErrPageGone):
		writeError(w, http.StatusBadGateway, "The shop says this page doesn't exist"+code+".")
	case errors.Is(err, sources.ErrBlocked):
		writeError(w, http.StatusBadGateway, "The shop refused to show this page to the monitor"+code+", so it can't be watched.")
	case st != nil:
		writeError(w, http.StatusBadGateway, "The shop answered with an error"+code+". Try again later.")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "The page took too long to load.")
	default:
		slog.Info("load page", "err", textutil.OneLine(err.Error()))
		writeError(w, http.StatusBadGateway, "Couldn't load the page: "+err.Error())
	}
}

// --- watches ------------------------------------------------------------------

type watchBody struct {
	Name            string                  `json:"name"`
	URL             string                  `json:"url"`
	Snapshot        string                  `json:"snapshot"` // the copy the price was picked on
	Extractor       sources.Extractor       `json:"extractor"`
	Rule            subscriptions.WatchRule `json:"rule"`
	IntervalMinutes int                     `json:"intervalMinutes"`
	ChannelID       int64                   `json:"channelId"`
}

// input turns a body into a watch input. With a snapshot, the reading
// (the watch's silent baseline) comes from that copy, so it is exactly what
// the user saw; live says to read the page now if there is none.
func (s *server) input(ctx context.Context, o subscriptions.Owner, b watchBody, live bool) (subscriptions.WatchInput, error) {
	in := subscriptions.WatchInput{
		Name: b.Name, URL: b.URL, Extractor: b.Extractor, Rule: b.Rule, IntervalMinutes: b.IntervalMinutes, ChannelID: b.ChannelID,
	}
	if p, ok := s.ownSnapshot(o, b.Snapshot); ok {
		res, err := pages.Extract(p, b.Extractor)
		if err != nil {
			return in, err
		}
		title, image := pages.About(p)
		in.URL = p.URL
		in.Reading = &sources.Reading{Price: res.Price, Raw: res.Raw, Title: title, ImageURL: image}
		return in, nil
	}
	if !live {
		return in, nil
	}
	url, err := pages.CheckURL(b.URL)
	if err != nil {
		return in, err
	}
	reading, err := s.Pages.ReadPrice(ctx, url, b.Extractor)
	if err != nil {
		return in, err
	}
	in.URL, in.Reading = url, &reading
	return in, nil
}

func (s *server) listWatches(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	vs, err := s.Watches.ListWatches(r.Context(), o, r.URL.Query().Get("all") == "1")
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, nonNil(vs))
}

func (s *server) createWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	var b watchBody
	if !decode(w, r, &b) {
		return
	}
	in, err := s.input(r.Context(), o, b, true)
	if s.failPage(w, err) {
		return
	}
	v, err := s.Watches.CreateWatch(r.Context(), o, in)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) updateWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	var b watchBody
	if !decode(w, r, &b) {
		return
	}
	in, err := s.input(r.Context(), o, b, false)
	if s.failPage(w, err) {
		return
	}
	v, err := s.Watches.UpdateWatch(r.Context(), o, id, in)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type watchDetail struct {
	Watch   subscriptions.WatchView    `json:"watch"`
	History []subscriptions.PricePoint `json:"history"` // newest first
	Alerts  []subscriptions.WatchAlert `json:"alerts"`
}

func (s *server) getWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	v, err := s.Watches.GetWatch(r.Context(), o, id)
	if s.fail(w, err) {
		return
	}
	history, alerts, err := s.Watches.WatchHistory(r.Context(), o, id, 500)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, watchDetail{Watch: v, History: nonNil(history), Alerts: nonNil(alerts)})
}

func (s *server) patchWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Enabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "only enabled can be patched")
		return
	}
	if s.fail(w, s.Watches.SetWatchEnabled(r.Context(), o, id, *b.Enabled)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	if s.fail(w, s.Watches.DeleteWatch(r.Context(), o, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) resetWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	v, err := s.Watches.ResetWatchReference(r.Context(), o, id)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// checkWatch reads the page now. A page that can't be read is not an error
// here: the watch shows the failure like any scheduled read.
func (s *server) checkWatch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := watchID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	v, err := s.Watches.CheckWatchNow(ctx, o, id, s.Pages)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// failPage writes a failure to read the picked price (an expired copy, a
// missing element) as the user's problem; anything else through fail.
func (s *server) failPage(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var inv *sources.InvalidSearch
	if errors.As(err, &inv) || errors.Is(err, sources.ErrNoPrice) {
		msg := err.Error()
		if inv != nil {
			msg = inv.Msg
		}
		writeError(w, http.StatusUnprocessableEntity, capitalize(msg)+".")
		return true
	}
	s.pageError(w, err)
	return true
}

func watchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such watch")
		return 0, false
	}
	return id, true
}

// decode reads a JSON body into v, strictly.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}
