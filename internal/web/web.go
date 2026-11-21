// Package web serves the dashboard: its JSON API (users' own searches,
// matches, the catalog to build search forms from, the admin overview) and
// the built single-page app. People log in with a one-time link from the
// chat bot; the session lives in an HttpOnly cookie.
package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Searches is what the dashboard needs from the subscriptions slice.
type Searches interface {
	Login(ctx context.Context, loginToken string) (string, subscriptions.Owner, error)
	SessionOwner(ctx context.Context, session string) (subscriptions.Owner, error)
	Logout(ctx context.Context, session string) error
	ListSearches(ctx context.Context, o subscriptions.Owner, all bool) ([]subscriptions.SearchView, error)
	CreateSearch(ctx context.Context, o subscriptions.Owner, in subscriptions.SearchInput) (subscriptions.SearchView, error)
	UpdateSearch(ctx context.Context, o subscriptions.Owner, id int64, in subscriptions.SearchInput) (subscriptions.SearchView, error)
	SetEnabled(ctx context.Context, o subscriptions.Owner, id int64, enabled bool) error
	DeleteSearch(ctx context.Context, o subscriptions.Owner, id int64) error
	Channels(ctx context.Context, o subscriptions.Owner) ([]subscriptions.ChannelView, error)
	RecentMatches(ctx context.Context, o subscriptions.Owner, searchID int64, limit int) ([]subscriptions.MatchRef, error)
	Categories() map[string][]sources.Category
	Preview(in subscriptions.SearchInput, candidates []events.ListingDiscovered) (subscriptions.Preview, error)
	CompileFeeds(in subscriptions.SearchInput) (source string, feeds []string, err error)
}

// Listings looks up stored ads: to show matches, to preview a search
// against, and to keep what an ad page adds.
type Listings interface {
	Summaries(ctx context.Context, source string, ids []string) (map[string]listings.Summary, error)
	Recent(ctx context.Context, source string, limit int32) ([]listings.Stored, error)
	Get(ctx context.Context, id int64) (listings.Stored, error)
	SaveDetails(ctx context.Context, before, enriched events.ListingDiscovered) error
	MarkGone(ctx context.Context, source, externalID string) error
}

type Config struct {
	Searches Searches
	Listings Listings
	Overview http.Handler // the admin overview (GET /api/overview)
	// Sources read an ad's own page for an admin (its region, gearbox, ...).
	Sources     sources.Registry
	WebDir      string // the built app; empty = API only
	PublicURL   string // https:// makes the cookie Secure
	MaxSearches int
	// Price watches; both nil turns them off.
	Watches    Watches
	Pages      Pages
	MaxWatches int
	// The admin's gem finder; nil or no source turns it off.
	Gems      Gems
	GemSource string
}

const (
	sessionCookie = "session"
	// csrfHeader must be on every request that changes something. A form or
	// a link on another site cannot set it; fetch() from our page does.
	csrfHeader = "X-Requested-With"
)

type server struct {
	Config
	secure bool
	watch  *watchState
	live   liveLimiter
}

type ctxKey struct{}

// New returns the dashboard's handler.
func New(cfg Config) http.Handler {
	s := &server{Config: cfg, secure: strings.HasPrefix(cfg.PublicURL, "https://")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.Handle("GET /api/me", s.auth(s.me))
	mux.Handle("GET /api/sources", s.auth(s.listSources))
	mux.Handle("GET /api/channels", s.auth(s.channels))
	mux.Handle("GET /api/searches", s.auth(s.listSearches))
	mux.Handle("POST /api/searches", s.auth(s.createSearch))
	mux.Handle("PUT /api/searches/{id}", s.auth(s.updateSearch))
	mux.Handle("PATCH /api/searches/{id}", s.auth(s.patchSearch))
	mux.Handle("DELETE /api/searches/{id}", s.auth(s.deleteSearch))
	mux.Handle("GET /api/matches", s.auth(s.matches))
	mux.Handle("POST /api/searches/preview", s.auth(s.previewSearch))
	mux.Handle("POST /api/searches/preview/live", s.auth(s.previewLive))
	mux.Handle("POST /api/listings/{id}/details", s.auth(s.admin(s.listingDetails)))
	s.routeWatches(mux)
	s.routeGems(mux)
	if cfg.Overview != nil {
		mux.Handle("GET /api/overview", s.auth(s.admin(func(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
			cfg.Overview.ServeHTTP(w, r)
		})))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "no such endpoint") })
	if cfg.WebDir != "" {
		mux.Handle("/", spa(cfg.WebDir))
	}
	return mux
}

// spa serves the built app, falling back to index.html for its own routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}
