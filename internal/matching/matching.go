// Package matching is the slice that decides which new listings concern
// which saved search, and remembers what each search was told. The matcher
// is its only writer: it owns the matches table. Searches themselves belong
// to the subscriptions slice; this one reads them through Searches.
//
// Schema lives in migrations/ (goose), queries in queries/ and the typed code
// in matchingdb/ is generated from both by sqlc: run `go generate ./...`.
package matching

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/matching/matchingdb"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/db"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Searches is what matching reads of the subscriptions slice.
type Searches interface {
	// EnabledSearches lists the searches watching source that are not paused.
	EnabledSearches(ctx context.Context, source string) ([]subscriptions.Search, error)
}

// AdPageHook hears about every ad page the matcher reads, so what it found
// is kept (by ingest, which owns the listings).
type AdPageHook func(ctx context.Context, read events.AdPageRead)

type Service struct {
	pool     *pgxpool.Pool
	q        *matchingdb.Queries
	searches Searches
	sources  sources.Registry

	adPage   AdPageHook                          // optional; see WithAdPageHook
	adPageOf func(events.ListingDiscovered) bool // optional; see WithAdPageFor
	peers    CarPeers                            // optional; see WithCarRating
}

func NewService(pool *pgxpool.Pool, searches Searches, reg sources.Registry) *Service {
	return &Service{pool: pool, q: matchingdb.New(pool), searches: searches, sources: reg}
}

// WithAdPageHook lets the caller keep what the matcher reads from ad pages
// (it reads them anyway for searches that filter on ad-page facts), so those
// facts are not thrown away after the alert.
func (s *Service) WithAdPageHook(h AdPageHook) *Service {
	s.adPage = h
	return s
}

// WithAdPageFor makes the matcher read the ad page of every matched listing
// that want accepts, not only of those a search filter needs it for. It is
// still read once per listing, and only when someone is told about it.
func (s *Service) WithAdPageFor(want func(events.ListingDiscovered) bool) *Service {
	s.adPageOf = want
	return s
}

// Migrate brings this slice's schema up to date (the matcher, its owner).
func (s *Service) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return db.Migrate(ctx, s.pool, "matching", sub)
}

// AwaitSchema waits until the matcher has migrated the schema this build
// reads.
func (s *Service) AwaitSchema(ctx context.Context) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return db.AwaitMigrated(ctx, s.pool, "matching", sub)
}

// Notification is a request to tell one search's user about a listing.
type Notification struct {
	SearchID int64
	events.NotificationRequested
}

// Handle matches one new listing: it hands every notification to publish,
// then records them. A crash in between re-publishes (the notifier dedups by
// Key) instead of silently losing the match.
func (s *Service) Handle(ctx context.Context, l events.ListingDiscovered, publish func(context.Context, events.NotificationRequested) error) error {
	ns, err := s.Notifications(ctx, l)
	if err != nil {
		return err
	}
	for _, n := range ns {
		if err := publish(ctx, n.NotificationRequested); err != nil {
			return err
		}
	}
	slog.Info("matched", "listing", l.ExternalID, "notifications", len(ns))
	if err := s.Record(ctx, ns); err != nil {
		return err
	}
	observeMatch(s.sources, l.Source, len(ns)) // once per listing, not per retry
	return nil
}

// Notifications returns a request for every search that matches l and was not
// told about it yet; car alerts carry their deal verdicts. It records
// nothing: see Handle.
func (s *Service) Notifications(ctx context.Context, l events.ListingDiscovered) ([]Notification, error) {
	src, err := s.sources.Get(l.Source)
	if err != nil {
		// Retrying cannot fix an unregistered source; drop it loudly.
		slog.Error("listing from unknown source", "source", l.Source, "id", l.ExternalID)
		return nil, nil
	}
	searches, err := s.searches.EnabledSearches(ctx, l.Source)
	if err != nil {
		return nil, err
	}
	done, err := s.q.ListMatchedSearchIDs(ctx, matchingdb.ListMatchedSearchIDsParams{Source: l.Source, ExternalID: l.ExternalID})
	if err != nil {
		return nil, err
	}

	var enricher sources.Enricher
	enrichable := map[string]bool{}
	if e, ok := src.(sources.Enricher); ok {
		enricher = e
		for _, k := range e.EnrichableKeys() {
			enrichable[k] = true
		}
	}

	// Pass 1 on what the feed page showed. Searches that pass every filter
	// they can check, but filter on something only the ad page has, wait for
	// pass 2.
	var matched, pending []subscriptions.Search
	for _, search := range searches {
		if slices.Contains(done, search.ID) {
			continue
		}
		switch search.Evaluate(src, l, enrichable) {
		case subscriptions.Matched:
			matched = append(matched, search)
		case subscriptions.NeedsDetail:
			pending = append(pending, search)
		}
	}

	// Pass 2: one ad-page read for this listing, shared by every waiting
	// search of every user.
	var unchecked []string // filters the unread ad page left unchecked
	if len(pending) > 0 {
		switch enriched, err := s.readAdPage(ctx, enricher, l); {
		case errors.Is(err, sources.ErrGone):
			matched = nil // removed: announced to no one
		case err != nil:
			// Better an unconfirmed alert than a missed deal: tell the
			// candidates, flagged, rather than failing (and so dropping) the
			// listing for everyone.
			unchecked = pendingKeys(pending, l, enrichable)
			matched = append(matched, pending...)
		default:
			l = enriched
			for _, search := range pending {
				if search.Evaluate(src, l, nil) == subscriptions.Matched {
					matched = append(matched, search)
				}
			}
		}
	} else if len(matched) > 0 && enricher != nil && s.adPageOf != nil && s.adPageOf(l) {
		// Pass 3: someone is told about it and the caller wants its page
		// anyway. Failing to read it only costs what the page would add, but
		// an ad that turns out removed is announced to no one.
		switch enriched, err := s.readAdPage(ctx, enricher, l); {
		case errors.Is(err, sources.ErrGone):
			matched = nil
		case err == nil:
			l = enriched
		}
	}
	if len(matched) == 0 {
		return nil, nil
	}

	// What the matcher adds, written once per language of those told.
	ratings, rated := s.rate(ctx, l)
	added := map[string][]events.Field{}
	addedIn := func(lang string) []events.Field {
		if fs, ok := added[lang]; ok {
			return fs
		}
		var fs []events.Field
		if len(unchecked) > 0 {
			fs = append(fs, uncheckedField(lang, unchecked))
		}
		if rated {
			fs = append(fs, deals.FieldsIn(ratings, deals.Lang(lang))...)
		}
		added[lang] = fs
		return fs
	}

	out := make([]Notification, 0, len(matched))
	for _, search := range matched {
		listing := l
		listing.Fields = slices.Concat(l.Fields, addedIn(search.Lang))
		out = append(out, Notification{SearchID: search.ID, NotificationRequested: events.NotificationRequested{
			Key:        fmt.Sprintf("%d:%s:%s", search.ID, l.Source, l.ExternalID),
			UserID:     search.UserID,
			SearchName: search.Name,
			Channel:    search.Channel,
			Listing:    &listing,
			Lang:       search.Lang,
		}})
	}
	return out, nil
}

// uncheckedField flags an alert whose ad page could not be read to check
// the filters keys.
func uncheckedField(lang string, keys []string) events.Field {
	f := events.Field{Name: "⚠️ Unconfirmed", Value: "Could not read the ad page to check: " + strings.Join(keys, ", ")}
	if lang == events.LangLatvian {
		f.Name, f.Value = "⚠️ Nav pārbaudīts", "Neizdevās nolasīt sludinājuma lapu, lai pārbaudītu: "+strings.Join(keys, ", ")
	}
	return f
}

// readAdPage reads l's ad page through enricher, counting and logging the
// outcome and telling the ad-page hook.
func (s *Service) readAdPage(ctx context.Context, enricher sources.Enricher, l events.ListingDiscovered) (events.ListingDiscovered, error) {
	enriched, err := enricher.Enrich(ctx, l)
	switch {
	case errors.Is(err, sources.ErrGone):
		slog.Info("ad removed before its page was read", "id", l.ExternalID)
		adPageReads.WithLabelValues(l.Source, "gone").Inc()
		if s.adPage != nil {
			s.adPage(ctx, events.AdPageRead{Listing: l, Gone: true})
		}
	case err != nil:
		slog.Warn("ad page unavailable", "id", l.ExternalID, "err", err)
		adPageReads.WithLabelValues(l.Source, "failed").Inc()
	default:
		adPageReads.WithLabelValues(l.Source, "ok").Inc()
		if s.adPage != nil {
			read := enriched
			s.adPage(ctx, events.AdPageRead{Listing: l, Read: &read})
		}
	}
	return enriched, err
}

// pendingKeys lists the filters that could not be checked, for the warning.
func pendingKeys(searches []subscriptions.Search, l events.ListingDiscovered, enrichable map[string]bool) []string {
	var keys []string
	for _, s := range searches {
		for _, m := range []map[string]bool{keysOf(s.Filters.Ranges), keysOf(s.Filters.OneOf), keysOf(s.Filters.AllOf)} {
			for k := range m {
				if _, has := l.Attrs[k]; enrichable[k] && !has {
					keys = append(keys, k)
				}
			}
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

func keysOf[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// Record marks the notifications as handed off.
func (s *Service) Record(ctx context.Context, ns []Notification) error {
	for _, n := range ns {
		err := s.q.RecordMatch(ctx, matchingdb.RecordMatchParams{
			SearchID: n.SearchID, Source: n.Listing.Source, ExternalID: n.Listing.ExternalID,
		})
		if err != nil {
			return fmt.Errorf("record match: %w", err)
		}
	}
	return nil
}

// maxRecent caps one Recent call.
const maxRecent = 1000

// Recent lists the latest matches of the given searches, newest first
// (SearchName left empty: the caller knows its searches).
func (s *Service) Recent(ctx context.Context, searchIDs []int64, limit int) ([]subscriptions.MatchRef, error) {
	maxRows := int32(maxRecent)
	if limit >= 0 && limit < maxRecent {
		maxRows = int32(limit) // bounded on this line, so the conversion cannot overflow
	}
	rows, err := s.q.RecentMatches(ctx, matchingdb.RecentMatchesParams{SearchIds: searchIDs, MaxRows: maxRows})
	if err != nil {
		return nil, err
	}
	out := make([]subscriptions.MatchRef, len(rows))
	for i, r := range rows {
		out[i] = subscriptions.MatchRef{SearchID: r.SearchID, Source: r.Source, ExternalID: r.ExternalID, At: r.CreatedAt.Time}
	}
	return out, nil
}

// Counts returns how many matches each of the searches has had.
func (s *Service) Counts(ctx context.Context, searchIDs []int64) (map[int64]int64, error) {
	rows, err := s.q.CountMatches(ctx, searchIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.SearchID] = r.Matches
	}
	return out, nil
}

// Purge forgets matches older than olderThan. Keep this longer than the
// listings retention, or a re-scraped old ad could be announced twice.
func (s *Service) Purge(ctx context.Context, olderThan time.Duration) (int64, error) {
	return s.q.PurgeMatches(ctx, olderThan.Seconds())
}
