// Package subscriptions is the slice that owns who wants what: users, their
// chat accounts and logins, delivery channels, saved searches, settings and
// price watches. It decides which scrape feeds are worth fetching. The api
// service is its only writer (the dashboard, the chat bot, the price-watch
// loop); the scraper and the matcher only read it. Which listing matched
// which search belongs to the matcher (internal/matching).
//
// Schema lives in migrations/ (goose), queries in queries/ and the typed code
// in subscriptionsdb/ is generated from both by sqlc: run `go generate ./...`.
package subscriptions

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/db"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions/subscriptionsdb"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Service struct {
	pool       *pgxpool.Pool
	q          *subscriptionsdb.Queries
	sources    sources.Registry
	collapseAt int

	maxSearches int             // per user; 0 = unlimited
	admins      map[string]bool // "platform:id" of chat accounts
	matches     MatchLog        // optional; see WithMatchLog

	// Price watches (see watches.go).
	maxWatches       int // per user; 0 = unlimited
	minWatchInterval int // minutes
	publicURL        string
}

// MatchLog is what the searches' views show of their matches. The matcher
// records them (internal/matching); this slice only reads them.
type MatchLog interface {
	// Counts returns how many matches each search had.
	Counts(ctx context.Context, searchIDs []int64) (map[int64]int64, error)
	// Recent lists the searches' latest matches, newest first, without
	// SearchName.
	Recent(ctx context.Context, searchIDs []int64, limit int) ([]MatchRef, error)
}

// WithMatchLog shows each search's matches (counts, the recent ones).
func (s *Service) WithMatchLog(m MatchLog) *Service {
	s.matches = m
	return s
}

// NewService. collapseAt is how many sibling feeds a source may merge into
// one broader request (see sources.Source.PlanFeeds).
func NewService(pool *pgxpool.Pool, reg sources.Registry, collapseAt int) *Service {
	return &Service{pool: pool, q: subscriptionsdb.New(pool), sources: reg, collapseAt: collapseAt}
}

// Migrate brings this slice's schema up to date (the api, its owner).
func (s *Service) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return db.Migrate(ctx, s.pool, "subscriptions", sub)
}

// AwaitSchema waits until the api has migrated the schema this build reads.
func (s *Service) AwaitSchema(ctx context.Context) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return db.AwaitMigrated(ctx, s.pool, "subscriptions", sub)
}

// ActiveFeeds returns the feeds of source worth scraping right now. The
// scraper calls it every cycle, so edited searches apply without a restart.
func (s *Service) ActiveFeeds(ctx context.Context, source string) ([]string, error) {
	src, err := s.sources.Get(source)
	if err != nil {
		return nil, err
	}
	wanted, err := s.q.ListEnabledFeeds(ctx, source)
	if err != nil {
		return nil, err
	}
	return src.PlanFeeds(wanted, s.collapseAt), nil
}

// EnabledSearches lists the searches watching source that are not paused:
// what the matcher checks every new listing against. A search whose stored
// filters no longer parse is skipped, loudly.
func (s *Service) EnabledSearches(ctx context.Context, source string) ([]Search, error) {
	rows, err := s.q.ListEnabledSearches(ctx, source)
	if err != nil {
		return nil, err
	}
	out := make([]Search, 0, len(rows))
	for _, r := range rows {
		search, err := searchFromRow(r)
		if err != nil {
			slog.Error("skipping search with bad filters", "search", r.ID, "err", err)
			continue
		}
		out = append(out, search)
	}
	return out, nil
}

func searchFromRow(r subscriptionsdb.ListEnabledSearchesRow) (Search, error) {
	s := Search{
		ID: r.ID, UserID: r.UserID, Name: r.Name, Source: r.Source, Feeds: r.Feeds,
		Channel: events.Channel{Kind: r.ChannelKind, Target: r.ChannelTarget}, Lang: alertLangOr(r.AlertLang),
	}
	if err := json.Unmarshal(r.Filters, &s.Filters); err != nil {
		return s, err
	}
	return s, nil
}
