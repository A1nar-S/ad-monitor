// Package dashboard serves the admin overview: totals, and one page of the
// collected listings or saved searches. Searching, filtering, sorting and
// paging all happen here, in SQL; the dashboard only renders the page.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PageSize is how many rows one overview page holds.
const PageSize = 20

// Query is what the overview table shows: one view, narrowed and sorted.
// Empty strings mean "any".
type Query struct {
	View     string // "listings" (default) or "searches"
	Text     string // matched case-insensitively against title, location, source / name, owner, source
	Source   string
	Category string // listings only: "cars", "flats" or "parts"
	Status   string // "active" or "inactive" (searches: enabled or paused)
	Sort     string // listings only: "newest" (default), "oldest" or "title"
	Page     int    // 1-based; clamped to the last page
}

var (
	views      = []string{"listings", "searches"}
	categories = []string{"", "cars", "flats", "parts"}
	statuses   = []string{"", "active", "inactive"}
	sorts      = []string{"newest", "oldest", "title"}
)

// ParseQuery reads a Query from the URL, rejecting values it does not know.
func ParseQuery(v url.Values) (Query, error) {
	q := Query{
		View:     v.Get("view"),
		Text:     strings.TrimSpace(v.Get("q")),
		Source:   v.Get("source"),
		Category: v.Get("category"),
		Status:   v.Get("status"),
		Sort:     v.Get("sort"),
		Page:     1,
	}
	if q.View == "" {
		q.View = "listings"
	}
	if q.Sort == "" {
		q.Sort = "newest"
	}
	for _, check := range []struct {
		name, value string
		allowed     []string
	}{
		{"view", q.View, views},
		{"category", q.Category, categories},
		{"status", q.Status, statuses},
		{"sort", q.Sort, sorts},
	} {
		if !contains(check.allowed, check.value) {
			return q, fmt.Errorf("unknown %s %q", check.name, check.value)
		}
	}
	if p := v.Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return q, fmt.Errorf("page must be a positive number")
		}
		q.Page = n
	}
	if len(q.Text) > 200 {
		q.Text = q.Text[:200]
	}
	return q, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type Loader func(context.Context, Query) (json.RawMessage, error)

func NewHandler(load Loader) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		q, err := ParseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		data, err := load(ctx, q)
		if err != nil {
			slog.Error("dashboard overview", "err", err)
			http.Error(w, "Dashboard data is unavailable. Check the database and migrations.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(data); err != nil {
			slog.Warn("dashboard response", "err", err)
		}
	})
	return mux
}

// overview is the response: the totals, and one page of the asked view.
type overview struct {
	UpdatedAt time.Time       `json:"updatedAt"`
	Stats     json.RawMessage `json:"stats"`
	View      string          `json:"view"`
	Sources   []string        `json:"sources"` // every source in the view, for the filter
	Total     int             `json:"total"`   // rows matching the query
	Page      int             `json:"page"`
	Pages     int             `json:"pages"`
	PageSize  int             `json:"pageSize"`
	Items     json.RawMessage `json:"items"`
}

func Load(pool *pgxpool.Pool) Loader {
	return func(ctx context.Context, q Query) (json.RawMessage, error) {
		out := overview{View: q.View, PageSize: PageSize}
		if err := pool.QueryRow(ctx, statsQuery).Scan(&out.UpdatedAt, &out.Stats); err != nil {
			return nil, fmt.Errorf("stats: %w", err)
		}

		v := listingsView
		if q.View == "searches" {
			v = searchesView
		}
		where, args := v.where(q)
		if err := pool.QueryRow(ctx, "SELECT COALESCE(array_agg(DISTINCT source ORDER BY source), '{}') FROM "+v.table).Scan(&out.Sources); err != nil {
			return nil, fmt.Errorf("sources: %w", err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+v.from+" WHERE "+where, args...).Scan(&out.Total); err != nil {
			return nil, fmt.Errorf("count: %w", err)
		}

		out.Pages = max(1, (out.Total+PageSize-1)/PageSize)
		out.Page = min(q.Page, out.Pages)
		args = append(args, PageSize, (out.Page-1)*PageSize)
		page := fmt.Sprintf(`
SELECT COALESCE(jsonb_agg(to_jsonb(p) - 'rn' ORDER BY p.rn), '[]'::jsonb) FROM (
  SELECT %s, row_number() OVER (ORDER BY %s) AS rn
  FROM %s WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d
) p`, v.columns, v.order(q.Sort), v.from, where, v.order(q.Sort), len(args)-1, len(args))
		if err := pool.QueryRow(ctx, page, args...).Scan(&out.Items); err != nil {
			return nil, fmt.Errorf("page: %w", err)
		}
		return json.Marshal(out)
	}
}

// A view is one table of the overview. Only fixed SQL goes into the query
// text; everything the user typed is a parameter.
type view struct {
	table   string // for the source list
	from    string
	columns string
	text    string // the expression the search box matches
	status  string // the boolean column "active" means
	order   func(sort string) string
}

var listingsView = view{
	table: "listings",
	from:  "listings",
	columns: `id, source, title, url, location, price, image_url AS "imageUrl",
    is_active AS active, first_seen_at AS "firstSeenAt",
    attrs || COALESCE(details, '{}'::jsonb) AS attrs, detailed_at AS "detailedAt",
    ` + categoryExpr + ` AS category,
    (source = 'ss.com' AND (location LIKE 'transport/cars%' OR location LIKE 'real-estate/%')) AS "hasAdPage"`,
	text:   `title || ' ' || location || ' ' || source`,
	status: "is_active",
	order: func(sort string) string {
		switch sort {
		case "oldest":
			return "first_seen_at ASC, id ASC"
		case "title":
			return "lower(title) ASC, id DESC"
		}
		return "first_seen_at DESC, id DESC"
	},
}

var searchesView = view{
	table: "searches",
	from: `searches JOIN users ON users.id = searches.user_id
    JOIN channels ON channels.id = searches.channel_id`,
	columns: `searches.id, searches.name, COALESCE(NULLIF(users.display_name, ''), users.name) AS "userName", searches.source,
    searches.enabled, searches.feeds, searches.filters, channels.kind AS channel,
    (SELECT count(*) FROM matches WHERE search_id = searches.id) AS matches`,
	text:   `searches.name || ' ' || COALESCE(NULLIF(users.display_name, ''), users.name) || ' ' || searches.source`,
	status: "searches.enabled",
	order:  func(string) string { return "searches.created_at DESC, searches.id DESC" },
}

// categoryExpr names a listing's category: from its ss.com path; every
// rrr.lt listing is a car part.
const categoryExpr = `CASE
      WHEN source = 'rrr.lt' THEN 'parts'
      WHEN source <> 'ss.com' THEN NULL
      WHEN location = 'transport/cars' OR location LIKE 'transport/cars/%' THEN 'cars'
      WHEN location = 'real-estate/flats' OR location LIKE 'real-estate/flats/%' THEN 'flats'
    END`

// where turns the query into SQL conditions and their parameters.
func (v view) where(q Query) (string, []any) {
	var conds []string
	var args []any
	arg := func(a any) string {
		args = append(args, a)
		return "$" + strconv.Itoa(len(args))
	}
	conds = append(conds, "TRUE")
	if q.Text != "" {
		conds = append(conds, "("+v.text+") ILIKE "+arg("%"+escapeLike(q.Text)+"%"))
	}
	if q.Source != "" {
		src := "source"
		if v.table == "searches" {
			src = "searches.source"
		}
		conds = append(conds, src+" = "+arg(q.Source))
	}
	if q.Status != "" {
		conds = append(conds, v.status+" = "+arg(q.Status == "active"))
	}
	if q.Category != "" && v.table == "listings" {
		conds = append(conds, "("+categoryExpr+") = "+arg(q.Category))
	}
	return strings.Join(conds, " AND "), args
}

// escapeLike makes s match literally inside a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

const statsQuery = `
SELECT now(), jsonb_build_object(
  'listings', (SELECT count(*) FROM listings),
  'activeListings', (SELECT count(*) FROM listings WHERE is_active),
  'newToday', (SELECT count(*) FROM listings WHERE first_seen_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  'searches', (SELECT count(*) FROM searches),
  'enabledSearches', (SELECT count(*) FROM searches WHERE enabled),
  'users', (SELECT count(*) FROM users),
  'matches', (SELECT count(*) FROM matches),
  'deliveries', (SELECT count(*) FROM deliveries)
)`
