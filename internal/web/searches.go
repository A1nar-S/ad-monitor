package web

// Searches: the catalog to build them from, their CRUD, a preview against
// stored ads, and the alerts they sent.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// sourceView is one site a search can watch, with what can be searched on it.
type sourceView struct {
	ID         string             `json:"id"` // "ss.com", "rrr.lt"
	Categories []sources.Category `json:"categories"`
}

// listSources lists the searchable sites, the default one first: the editor's
// tabs.
func (s *server) listSources(w http.ResponseWriter, _ *http.Request, _ subscriptions.Owner) {
	cats, def := s.Searches.Categories(), s.Searches.DefaultSource()
	out := make([]sourceView, 0, len(cats))
	for name, cs := range cats {
		if len(cs) > 0 {
			out = append(out, sourceView{ID: name, Categories: cs})
		}
	}
	slices.SortFunc(out, func(a, b sourceView) int {
		if da, db := a.ID == def, b.ID == def; da != db {
			if da {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *server) channels(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	chs, err := s.Searches.Channels(r.Context(), o)
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, nonNil(chs))
}

func (s *server) listSearches(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	vs, err := s.Searches.ListSearches(r.Context(), o, r.URL.Query().Get("all") == "1")
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, nonNil(vs))
}

type searchBody struct {
	Name string `json:"name"`
	// Source is the site; empty means the default source (and, on an
	// update, the site the search already watches).
	Source    string         `json:"source"`
	Category  string         `json:"category"`
	Filters   map[string]any `json:"filters"`
	ChannelID int64          `json:"channelId"`
	Enabled   *bool          `json:"enabled"`
}

func (s *server) createSearch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	v, err := s.Searches.CreateSearch(r.Context(), o, subscriptions.SearchInput{
		Name: b.Name, Source: b.Source, Category: b.Category, Filters: b.Filters, ChannelID: b.ChannelID, Origin: "web",
	})
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) updateSearch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	v, err := s.Searches.UpdateSearch(r.Context(), o, id, subscriptions.SearchInput{
		Name: b.Name, Source: b.Source, Category: b.Category, Filters: b.Filters, ChannelID: b.ChannelID,
	})
	if s.fail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// patchSearch pauses or resumes.
func (s *server) patchSearch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	if b.Enabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "only enabled can be patched")
		return
	}
	if s.fail(w, s.Searches.SetEnabled(r.Context(), o, id, *b.Enabled)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteSearch(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if s.fail(w, s.Searches.DeleteSearch(r.Context(), o, id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type matchView struct {
	subscriptions.MatchRef
	Listing *listings.Summary `json:"listing,omitempty"` // nil once purged
}

// matches lists the owner's latest alerts with the ads they were about.
func (s *server) matches(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	refs, err := s.Searches.RecentMatches(r.Context(), o, 0, limit)
	if s.fail(w, err) {
		return
	}
	ids := map[string][]string{}
	for _, m := range refs {
		ids[m.Source] = append(ids[m.Source], m.ExternalID)
	}
	found := map[string]map[string]listings.Summary{}
	for source, list := range ids {
		if found[source], err = s.Listings.Summaries(r.Context(), source, list); s.fail(w, err) {
			return
		}
	}
	out := make([]matchView, len(refs))
	for i, m := range refs {
		out[i] = matchView{MatchRef: m}
		if l, ok := found[m.Source][m.ExternalID]; ok {
			out[i].Listing = &l
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// previewLimit is how many of the newest stored ads a preview checks. Enough
// for days of a busy feed; each check is a cheap in-memory comparison.
const previewLimit = 5000

// previewShown caps the ads a preview lists; the counts cover all of them.
const previewShown = 30

type previewAd struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Price     string    `json:"price"`
	ImageURL  string    `json:"imageUrl"`
	Location  string    `json:"location"`
	FirstSeen time.Time `json:"firstSeen"`
	Active    bool      `json:"active"`
	// Sure is false when the ad passes every list-page filter but its ad page
	// was never read to check the rest (see previewView.UnsureKeys).
	Sure bool `json:"sure"`
}

type previewView struct {
	// Live: the ads were just read from the site, not from the store.
	Live       bool        `json:"live,omitempty"`
	Scanned    int         `json:"scanned"` // stored ads looked at
	InScope    int         `json:"inScope"` // of those, in the search's category and area
	Matched    int         `json:"matched"`
	Unsure     int         `json:"unsure"`
	UnsureKeys []string    `json:"unsureKeys"`
	Since      *time.Time  `json:"since,omitempty"` // the oldest ad looked at
	Ads        []previewAd `json:"ads"`             // newest first
}

// previewSearch shows which already collected ads a search, as it is being
// edited, would have matched. Nothing is saved.
func (s *server) previewSearch(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	source := b.Source
	if source == "" {
		source = s.Searches.DefaultSource()
	}
	stored, err := s.Listings.Recent(r.Context(), source, previewLimit)
	if s.fail(w, err) {
		return
	}
	candidates := make([]events.ListingDiscovered, len(stored))
	for i, st := range stored {
		candidates[i] = st.Listing
	}
	p, err := s.Searches.Preview(subscriptions.SearchInput{Source: source, Category: b.Category, Filters: b.Filters}, candidates)
	if s.fail(w, err) {
		return
	}
	out := previewView{
		Scanned: len(stored), InScope: p.InScope, Matched: len(p.Matched), Unsure: len(p.Unsure),
		UnsureKeys: nonNil(p.UnsureKeys), Ads: []previewAd{},
	}
	if n := len(stored); n > 0 {
		out.Since = &stored[n-1].FirstSeen
	}
	// Indexes follow the store's newest-first order, so sorting the merged
	// list keeps it newest first.
	sure := make(map[int]bool, len(p.Matched))
	for _, i := range p.Matched {
		sure[i] = true
	}
	picked := append(append([]int(nil), p.Matched...), p.Unsure...)
	slices.Sort(picked)
	for _, i := range picked[:min(len(picked), previewShown)] {
		st := stored[i]
		out.Ads = append(out.Ads, previewAd{
			ID: st.ID, Title: st.Listing.Title, URL: st.Listing.URL, Price: st.Listing.Price,
			ImageURL: st.Listing.ImageURL, Location: st.Listing.Location, FirstSeen: st.FirstSeen,
			Active: st.Active, Sure: sure[i],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func readBody(w http.ResponseWriter, r *http.Request) (searchBody, bool) {
	var b searchBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return b, false
	}
	return b, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such search")
		return 0, false
	}
	return id, true
}
