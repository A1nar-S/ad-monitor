// Package gems is the slice behind the gem finder: a full scan of every
// listing of one category for sale on a source, each one rated, best buys
// first. It is generic: a Category (cars: internal/gems/cars) rates and
// says what can be filtered; this package scans, stores, filters and lists.
// It owns the scan requests and the latest scan's ratings; the scraper runs
// scans (Scanner), the API requests (admins) and lists them (everyone).
//
// Schema lives in migrations/ (goose), queries in queries/ and the typed code
// in gemsdb/ is generated from both by sqlc: run `go generate ./...`.
package gems

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/gemsdb"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/db"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Service requests scans and reads their results, for the categories it
// was given.
type Service struct {
	pool *pgxpool.Pool
	q    *gemsdb.Queries
	cats []Category
}

func NewService(pool *pgxpool.Pool, cats ...Category) *Service {
	return &Service{pool: pool, q: gemsdb.New(pool), cats: cats}
}

// Categories are the ones this service rates, in the order given.
func (s *Service) Categories() []Category { return s.cats }

// Category returns the category with id.
func (s *Service) Category(id string) (Category, bool) {
	i := slices.IndexFunc(s.cats, func(c Category) bool { return c.ID() == id })
	if i < 0 {
		return nil, false
	}
	return s.cats[i], true
}

// Migrate brings this slice's schema up to date.
func (s *Service) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	return db.Migrate(ctx, s.pool, "gems", sub)
}

// ErrBusy: a scan of the source is already waiting or running.
var ErrBusy = errors.New("a scan is already waiting or running")

// ErrBadQuery: a filter or category the API cannot take.
var ErrBadQuery = errors.New("bad query")

// Mode is what a scan does.
type Mode string

const (
	// Full reads every list page of the category again (new and changed
	// listings, sold ones marked), then rates and reads ad pages as Rate.
	Full Mode = "full"
	// Rate rates the stored listings again, reads the ad pages of the
	// potential buys not read yet, and rates again: after the rating
	// changed, or to finish a scan cut short.
	Rate Mode = "rate"
)

// Modes are the scans there are.
var Modes = []Mode{Full, Rate}

// Scan is one scan request and how far it got.
type Scan struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	Category    string     `json:"category"`
	Mode        Mode       `json:"mode"`
	Status      string     `json:"status"` // requested, running, done, failed
	RequestedAt time.Time  `json:"requestedAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Pages       int        `json:"pages"`
	Ads         int        `json:"ads"`
	Gone        int        `json:"gone"`  // ads no longer on the site, marked sold
	Rated       int        `json:"rated"` // listings with a verdict
	Error       string     `json:"error,omitempty"`
	// Complete: the crawl reached the end and sold ones were marked. Note
	// says why not. Only a Full scan crawls: a Rate one is never complete.
	Complete bool   `json:"complete"`
	Note     string `json:"note,omitempty"`
	// Phase is listing (pass 1: every listing) or detailing (pass 2: the
	// ad pages of DetailTotal potential buys, Detailed read so far).
	Phase       string `json:"phase,omitempty"`
	DetailTotal int    `json:"detailTotal"`
	Detailed    int    `json:"detailed"`
}

func scanOf(r gemsdb.GemScan) Scan {
	t := func(ts pgtype.Timestamptz) *time.Time {
		if !ts.Valid {
			return nil
		}
		return &ts.Time
	}
	return Scan{
		ID: r.ID, Source: r.Source, Category: r.Category, Mode: Mode(r.Mode), Status: r.Status, RequestedAt: r.RequestedAt.Time,
		StartedAt: t(r.StartedAt), FinishedAt: t(r.FinishedAt),
		Pages: int(r.Pages), Ads: int(r.Ads), Gone: int(r.Gone), Rated: int(r.Rated), Error: r.Error,
		Complete: r.Complete, Note: r.Note,
		Phase: r.Phase, DetailTotal: int(r.DetailTotal), Detailed: int(r.Detailed),
	}
}

func (s *Service) known(category string) error {
	if _, ok := s.Category(category); !ok {
		return fmt.Errorf("%w: unknown category %q", ErrBadQuery, category)
	}
	return nil
}

// RequestScan asks the scraper to scan source's category; ErrBusy if a
// scan of it (of either mode) is pending.
func (s *Service) RequestScan(ctx context.Context, source, category string, mode Mode) (Scan, error) {
	if err := s.known(category); err != nil {
		return Scan{}, err
	}
	if !slices.Contains(Modes, mode) {
		return Scan{}, fmt.Errorf("%w: unknown scan mode %q", ErrBadQuery, mode)
	}
	var scan Scan
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockScans(ctx, gemsdb.LockScansParams{Source: source, Category: category}); err != nil {
			return err
		}
		r, err := q.RequestScan(ctx, gemsdb.RequestScanParams{Source: source, Category: category, Mode: string(mode)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBusy
		}
		scan = scanOf(r)
		return err
	})
	return scan, err
}

// LatestScan returns the newest scan of source's category; ok is false if
// there was none.
func (s *Service) LatestScan(ctx context.Context, source, category string) (Scan, bool, error) {
	if err := s.known(category); err != nil {
		return Scan{}, false, err
	}
	r, err := s.q.LatestScan(ctx, gemsdb.LatestScanParams{Source: source, Category: category})
	if errors.Is(err, pgx.ErrNoRows) {
		return Scan{}, false, nil
	}
	if err != nil {
		return Scan{}, false, err
	}
	return scanOf(r), true, nil
}

// Gem is one rated listing.
type Gem struct {
	ExternalID string         `json:"externalId"`
	Heading    string         `json:"heading"` // what it is, e.g. "BMW 520"
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	ImageURL   string         `json:"imageUrl"`
	Price      string         `json:"price"`
	PriceValue float64        `json:"priceValue"`
	PriceDiff  *float64       `json:"priceDiff"` // vs similar listings: -0.14 is 14% below; null = not compared
	Buy        string         `json:"buy"`       // good, average, bad, check
	Score      int            `json:"score"`     // 0-100
	Overall    string         `json:"overall"`
	Fields     []events.Field `json:"fields"`   // the verdict lines, as in an alert
	Lines      []Line         `json:"lines"`    // the same but the overall one, in parts
	Detailed   bool           `json:"detailed"` // its ad page was read
	// Components are the score's parts; Attrs the values of the category's
	// Spec fields (unknown left out).
	Components []Component    `json:"components"`
	Attrs      map[string]any `json:"attrs"`
}

// Buy values: the verdicts every category gives.
var buys = []string{"good", "average", "bad", "check"}

// The orders every category has: best buy first, cheapest, furthest below
// similar listings. A category adds its own (Spec.Sorts).
var commonSorts = []string{"", "price", "discount"}

// Query filters and orders the gems. Zero values match everything.
type Query struct {
	Buy                string // one of buys
	MinPrice, MaxPrice float64
	DetailedOnly       bool
	// Choices: a choice field's key → the values any of which it must have.
	Choices map[string][]string
	// Ranges: a range field's key → from and to; 0 = open.
	Ranges map[string][2]float64
	Sort   string // one of commonSorts or the category's
	Page   int    // from 1
	// Lang is the reader's language: the gems' texts in it where they have
	// them, else in English.
	Lang string
}

// PageSize is how many gems a page holds.
const PageSize = 24

// Page is one page of gems, with what the filters can offer.
type Page struct {
	Items    []Gem `json:"items"`
	Total    int   `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	// Facets: each choice field's key → the values the gems the other
	// filters let through have; Buys the same for the verdicts.
	Facets map[string][]Facet `json:"facets"`
	Buys   []Facet            `json:"buys"`
	// Detailed of All gems had their ad page read: only those have the
	// Detail fields to filter on.
	Detailed int `json:"detailed"`
	All      int `json:"all"`
}

// Facet is one value of a choice and how many gems have it; Parent is the
// value of the choice it narrows (a model's make).
type Facet struct {
	Value  string `json:"value"`
	Parent string `json:"parent,omitempty"`
	Count  int    `json:"count"`
}

// List returns one page of the gems of source's category.
func (s *Service) List(ctx context.Context, source, category string, q Query) (Page, error) {
	cat, ok := s.Category(category)
	if !ok {
		return Page{}, fmt.Errorf("%w: unknown category %q", ErrBadQuery, category)
	}
	spec := cat.Spec()
	if q.Buy != "" && !slices.Contains(buys, q.Buy) {
		return Page{}, fmt.Errorf("%w: buy must be one of %v", ErrBadQuery, buys)
	}
	var sortKey string
	var sortDesc bool
	if !slices.Contains(commonSorts, q.Sort) {
		i := slices.IndexFunc(spec.Sorts, func(x Sort) bool { return x.ID == q.Sort })
		if i < 0 {
			return Page{}, fmt.Errorf("%w: unknown sort %q", ErrBadQuery, q.Sort)
		}
		sortKey, sortDesc = spec.Sorts[i].Key, spec.Sorts[i].Desc
	}
	choices := map[string][]string{}
	for k, v := range q.Choices {
		if f, ok := spec.field(k); !ok || f.Kind != Choice {
			return Page{}, fmt.Errorf("%w: %q is not a choice", ErrBadQuery, k)
		}
		if len(v) > 0 {
			choices[k] = v
		}
	}
	ranges := map[string][2]float64{}
	for k, v := range q.Ranges {
		if f, ok := spec.field(k); !ok || f.Kind != Range {
			return Page{}, fmt.Errorf("%w: %q is not a range", ErrBadQuery, k)
		}
		if v[0] > 0 || v[1] > 0 {
			ranges[k] = v
		}
	}
	cj, _ := json.Marshal(choices)
	rj, _ := json.Marshal(ranges)
	if q.Page < 1 {
		q.Page = 1
	}

	rows, err := s.q.ListGems(ctx, gemsdb.ListGemsParams{
		Source: source, Category: category, Buy: q.Buy, MinPrice: q.MinPrice, MaxPrice: q.MaxPrice,
		DetailedOnly: q.DetailedOnly, Choices: cj, Ranges: rj,
		Sort: q.Sort, SortKey: sortKey, SortDesc: sortDesc,
		Off: int32((q.Page - 1) * PageSize), Lim: PageSize,
	})
	if err != nil {
		return Page{}, err
	}
	total, err := s.q.CountGems(ctx, gemsdb.CountGemsParams{
		Source: source, Category: category, Buy: q.Buy, MinPrice: q.MinPrice, MaxPrice: q.MaxPrice,
		DetailedOnly: q.DetailedOnly, Choices: cj, Ranges: rj,
	})
	if err != nil {
		return Page{}, err
	}
	facets, err := s.facets(ctx, spec, gemsdb.GemFacetsParams{
		Source: source, Category: category, Buy: q.Buy, MinPrice: q.MinPrice, MaxPrice: q.MaxPrice,
		DetailedOnly: q.DetailedOnly, Choices: cj, Ranges: rj,
	})
	if err != nil {
		return Page{}, err
	}
	buyRows, err := s.q.GemBuyCounts(ctx, gemsdb.GemBuyCountsParams{
		Source: source, Category: category, MinPrice: q.MinPrice, MaxPrice: q.MaxPrice,
		DetailedOnly: q.DetailedOnly, Choices: cj, Ranges: rj,
	})
	if err != nil {
		return Page{}, err
	}
	counts, err := s.q.DetailedCount(ctx, gemsdb.DetailedCountParams{Source: source, Category: category})
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: make([]Gem, 0, len(rows)), Total: int(total), Page: q.Page, PageSize: PageSize, Facets: facets,
		Buys: make([]Facet, 0, len(buyRows)), Detailed: int(counts.Detailed), All: int(counts.Total)}
	for _, r := range buyRows {
		p.Buys = append(p.Buys, Facet{Value: r.Buy, Count: int(r.N)})
	}
	for _, r := range rows {
		g := Gem{
			ExternalID: r.ExternalID, Heading: r.Heading, Title: r.Title, URL: r.Url, ImageURL: r.ImageUrl,
			Price: r.Price, PriceValue: r.PriceValue, PriceDiff: r.PriceDiff, Buy: r.Buy, Score: int(r.DisplayScore),
			Overall: r.Overall, Detailed: r.Detailed,
			Fields: []events.Field{}, Lines: []Line{}, Components: []Component{}, Attrs: map[string]any{},
		}
		_ = json.Unmarshal(r.Fields, &g.Fields)
		_ = json.Unmarshal(r.Lines, &g.Lines)
		_ = json.Unmarshal(r.Components, &g.Components)
		_ = json.Unmarshal(r.Attrs, &g.Attrs)
		if q.Lang != "" {
			g.translate(r.Translations, q.Lang)
		}
		p.Items = append(p.Items, g)
	}
	return p, nil
}

// facetKey is how GemFacets counts one choice: Parent is the choice it
// narrows, Skip the choices its counts ignore.
type facetKey struct {
	Parent string   `json:"parent"`
	Skip   []string `json:"skip"`
}

// facets counts each choice's values among the gems that pass every other
// filter: a choice ignores itself (so its other values stay offered) and
// the choices it narrows' children (picking a make again clears the model).
// translate puts g's texts in lang, from the stored translations, where
// they have it.
func (g *Gem) translate(stored []byte, lang string) {
	var tr map[string]Text
	if json.Unmarshal(stored, &tr) != nil {
		return
	}
	t, ok := tr[lang]
	if !ok {
		return
	}
	g.Overall = t.Overall
	if t.Fields != nil {
		g.Fields = t.Fields
	}
	if t.Lines != nil {
		g.Lines = t.Lines
	}
	for i, c := range g.Components {
		if why, ok := t.Why[c.Key]; ok {
			g.Components[i].Why = why
		}
	}
}

func (s *Service) facets(ctx context.Context, spec Spec, p gemsdb.GemFacetsParams) (map[string][]Facet, error) {
	keys := map[string]facetKey{}
	out := map[string][]Facet{}
	for _, f := range spec.Fields {
		if f.Kind != Choice {
			continue
		}
		k := facetKey{Parent: f.DependsOn, Skip: []string{f.Key}}
		for _, child := range spec.Fields {
			if child.DependsOn == f.Key {
				k.Skip = append(k.Skip, child.Key)
			}
		}
		keys[f.Key] = k
		out[f.Key] = []Facet{}
	}
	p.Keys, _ = json.Marshal(keys)
	rows, err := s.q.GemFacets(ctx, p)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Key] = append(out[r.Key], Facet{Value: r.Value, Parent: r.Parent, Count: int(r.N)})
	}
	return out, nil
}
