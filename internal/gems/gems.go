// Package gems is the slice behind the gem finder: a full scan of every car
// for sale on a source, each one rated (internal/deals), best buys first.
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
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/gemsdb"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/db"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Service requests scans and reads their results.
type Service struct {
	pool *pgxpool.Pool
	q    *gemsdb.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: gemsdb.New(pool)}
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

// Scan is one scan request and how far it got.
type Scan struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	Status      string     `json:"status"` // requested, running, done, failed
	RequestedAt time.Time  `json:"requestedAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Pages       int        `json:"pages"`
	Ads         int        `json:"ads"`
	Gone        int        `json:"gone"`  // ads no longer on the site, marked sold
	Rated       int        `json:"rated"` // cars with a verdict
	Error       string     `json:"error,omitempty"`
	// Complete: the crawl reached the end and sold cars were marked. Note
	// says why not.
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
		ID: r.ID, Source: r.Source, Status: r.Status, RequestedAt: r.RequestedAt.Time,
		StartedAt: t(r.StartedAt), FinishedAt: t(r.FinishedAt),
		Pages: int(r.Pages), Ads: int(r.Ads), Gone: int(r.Gone), Rated: int(r.Rated), Error: r.Error,
		Complete: r.Complete, Note: r.Note,
		Phase: r.Phase, DetailTotal: int(r.DetailTotal), Detailed: int(r.Detailed),
	}
}

// RequestScan asks the scraper to scan source; ErrBusy if one is pending.
func (s *Service) RequestScan(ctx context.Context, source string) (Scan, error) {
	var scan Scan
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockSource(ctx, source); err != nil {
			return err
		}
		r, err := q.RequestScan(ctx, source)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBusy
		}
		scan = scanOf(r)
		return err
	})
	return scan, err
}

// LatestScan returns source's newest scan; ok is false if there was none.
func (s *Service) LatestScan(ctx context.Context, source string) (Scan, bool, error) {
	r, err := s.q.LatestScan(ctx, source)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scan{}, false, nil
	}
	if err != nil {
		return Scan{}, false, err
	}
	return scanOf(r), true, nil
}

// Categories are what the finder can scan a source for.
var Categories = []string{"cars"}

// Gem is one rated car.
type Gem struct {
	ExternalID string         `json:"externalId"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	ImageURL   string         `json:"imageUrl"`
	Make       string         `json:"make"`
	Model      string         `json:"model"`
	Year       int            `json:"year"`
	MileageKm  int            `json:"mileageKm"`  // 0 = unknown
	EngineL    float64        `json:"engineL"`    // 0 = unknown
	Fuel       string         `json:"fuel"`       // internal/deals' words: petrol, diesel, ...
	Gearbox    string         `json:"gearbox"`    // automatic, manual; "" = unknown (ad page not read)
	Inspection string         `json:"inspection"` // valid, none; "" = unknown
	Body       string         `json:"body"`       // sedan, wagon, ...; "" = unknown
	Region     string         `json:"region"`     // the site's words, e.g. "Rīga"; "" = unknown
	Detailed   bool           `json:"detailed"`   // its ad page was read
	Price      string         `json:"price"`
	PriceValue float64        `json:"priceValue"`
	PriceDiff  *float64       `json:"priceDiff"` // vs similar ads: -0.14 is 14% below; null = not compared
	Buy        string         `json:"buy"`       // good, average, bad, check
	Score      int            `json:"score"`     // 0-100
	Overall    string         `json:"overall"`
	Fields     []events.Field `json:"fields"` // the verdict lines, as in an alert
	// Components are the score's parts (price, mileage, engine, ...).
	Components []deals.Component `json:"components"`
}

// Buy values, the deals.Buy verdicts in the API's words.
var buys = []string{"good", "average", "bad", "check"}

// Sorts the list takes: best buy first, cheapest, newest model year, fewest
// km, furthest below similar ads.
var sorts = []string{"", "price", "year", "mileage", "discount"}

// Query filters and orders the gems. Zero values match everything; a list
// matches any of its values.
type Query struct {
	Make, Model            string
	Buy                    string // one of buys
	Fuels, Gearboxes       []string
	Bodies, Regions        []string
	MinPrice, MaxPrice     float64
	MinYear, MaxYear       int
	MaxMileage             int
	MinEngineL, MaxEngineL float64
	DetailedOnly           bool
	InspectedOnly          bool   // a valid technical inspection
	Sort                   string // one of sorts
	Page                   int    // from 1
}

// PageSize is how many gems a page holds.
const PageSize = 24

// Page is one page of gems, with what the filters can offer.
type Page struct {
	Items    []Gem  `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
	Facets   Facets `json:"facets"`
	// Detailed of All gems had their ad page read: only those have a
	// gearbox, body and region to filter on.
	Detailed int `json:"detailed"`
	All      int `json:"all"`
}

// Facets are the values each filter can take among the gems, with counts.
type Facets struct {
	Models    []MakeModel `json:"models"`
	Fuels     []Count     `json:"fuels"`
	Gearboxes []Count     `json:"gearboxes"`
	Bodies    []Count     `json:"bodies"`
	Regions   []Count     `json:"regions"`
}

// MakeModel is a make and model among the gems, for the filters.
type MakeModel struct {
	Make  string `json:"make"`
	Model string `json:"model"`
	Count int    `json:"count"`
}

// Count is one value of a filter and how many gems have it.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// ErrBadQuery: a filter the API cannot take.
var ErrBadQuery = errors.New("bad query")

// List returns one page of source's gems.
func (s *Service) List(ctx context.Context, source string, q Query) (Page, error) {
	if q.Buy != "" && !slices.Contains(buys, q.Buy) {
		return Page{}, fmt.Errorf("%w: buy must be one of %v", ErrBadQuery, buys)
	}
	if !slices.Contains(sorts, q.Sort) {
		return Page{}, fmt.Errorf("%w: sort must be one of %v", ErrBadQuery, sorts[1:])
	}
	if q.Page < 1 {
		q.Page = 1
	}
	list := func(v []string) []string { // sqlc sends nil as NULL, which cardinality() does not count
		if v == nil {
			return []string{}
		}
		return v
	}
	rows, err := s.q.ListGems(ctx, gemsdb.ListGemsParams{
		Source: source, Make: q.Make, Model: q.Model, Buy: q.Buy,
		Fuels: list(q.Fuels), Gearboxes: list(q.Gearboxes), Bodies: list(q.Bodies), Regions: list(q.Regions),
		MinPrice: q.MinPrice, MaxPrice: q.MaxPrice, MinYear: int32(q.MinYear), MaxYear: int32(q.MaxYear),
		MaxMileage: int32(q.MaxMileage), MinEngine: q.MinEngineL, MaxEngine: q.MaxEngineL, DetailedOnly: q.DetailedOnly, InspectedOnly: q.InspectedOnly,
		Sort: q.Sort, Off: int32((q.Page - 1) * PageSize), Lim: PageSize,
	})
	if err != nil {
		return Page{}, err
	}
	total, err := s.q.CountGems(ctx, gemsdb.CountGemsParams{
		Source: source, Make: q.Make, Model: q.Model, Buy: q.Buy,
		Fuels: list(q.Fuels), Gearboxes: list(q.Gearboxes), Bodies: list(q.Bodies), Regions: list(q.Regions),
		MinPrice: q.MinPrice, MaxPrice: q.MaxPrice, MinYear: int32(q.MinYear), MaxYear: int32(q.MaxYear),
		MaxMileage: int32(q.MaxMileage), MinEngine: q.MinEngineL, MaxEngine: q.MaxEngineL, DetailedOnly: q.DetailedOnly, InspectedOnly: q.InspectedOnly,
	})
	if err != nil {
		return Page{}, err
	}
	facets, err := s.facets(ctx, source)
	if err != nil {
		return Page{}, err
	}
	counts, err := s.q.DetailedCount(ctx, source)
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: make([]Gem, 0, len(rows)), Total: int(total), Page: q.Page, PageSize: PageSize, Facets: facets,
		Detailed: int(counts.Detailed), All: int(counts.Total)}
	for _, r := range rows {
		g := Gem{
			ExternalID: r.ExternalID, Title: r.Title, URL: r.Url, ImageURL: r.ImageUrl, Make: r.Make, Model: r.Model,
			Year: int(r.Year), MileageKm: int(r.MileageKm), EngineL: r.EngineL, Fuel: r.Fuel, Gearbox: r.Gearbox, Inspection: r.Inspection,
			Body: r.Body, Region: r.Region, Detailed: r.Detailed, Price: r.Price, PriceValue: r.PriceValue,
			PriceDiff: r.PriceDiff, Buy: r.Buy, Score: int(r.DisplayScore), Overall: r.Overall,
			Fields: []events.Field{}, Components: []deals.Component{},
		}
		_ = json.Unmarshal(r.Fields, &g.Fields)
		_ = json.Unmarshal(r.Components, &g.Components)
		p.Items = append(p.Items, g)
	}
	return p, nil
}

func (s *Service) facets(ctx context.Context, source string) (Facets, error) {
	rows, err := s.q.GemFacets(ctx, source)
	if err != nil {
		return Facets{}, err
	}
	f := Facets{Models: []MakeModel{}, Fuels: []Count{}, Gearboxes: []Count{}, Bodies: []Count{}, Regions: []Count{}}
	for _, r := range rows {
		c := Count{Value: r.Value, Count: int(r.N)}
		switch r.Kind {
		case "model":
			mk, model, _ := strings.Cut(r.Value, "|")
			f.Models = append(f.Models, MakeModel{Make: mk, Model: model, Count: c.Count})
		case "fuel":
			f.Fuels = append(f.Fuels, c)
		case "gearbox":
			f.Gearboxes = append(f.Gearboxes, c)
		case "body":
			f.Bodies = append(f.Bodies, c)
		case "region":
			f.Regions = append(f.Regions, c)
		}
	}
	return f, nil
}
