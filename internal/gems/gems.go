// Package gems is the slice behind the admin's gem finder: a full scan of
// every car for sale on a source, each one rated (internal/deals), best buys
// first. It owns the scan requests and the latest scan's ratings; the
// scraper runs scans (Scanner), the API requests and lists them (Service).
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

// Gem is one rated car.
type Gem struct {
	ExternalID string         `json:"externalId"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	ImageURL   string         `json:"imageUrl"`
	Make       string         `json:"make"`
	Model      string         `json:"model"`
	Year       int            `json:"year"`
	MileageKm  int            `json:"mileageKm"` // 0 = unknown
	Fuel       string         `json:"fuel"`
	Price      string         `json:"price"`
	PriceValue float64        `json:"priceValue"`
	Buy        string         `json:"buy"` // good, average, bad, check
	Overall    string         `json:"overall"`
	Fields     []events.Field `json:"fields"` // the verdict lines, as in an alert
}

// Buy values, the deals.Buy verdicts in the API's words.
var buys = []string{"good", "average", "bad", "check"}

// Query filters and orders the gems. Zero values match everything.
type Query struct {
	Make, Model string
	Buy         string // one of buys
	MaxPrice    float64
	MinYear     int
	MaxMileage  int
	Sort        string // "" (best buy first), "price", "year"
	Page        int    // from 1
}

// PageSize is how many gems a page holds.
const PageSize = 30

// Page is one page of gems, with what the filters can offer.
type Page struct {
	Items    []Gem       `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
	Models   []MakeModel `json:"models"`
}

// MakeModel is a make and model among the gems, for the filters.
type MakeModel struct {
	Make  string `json:"make"`
	Model string `json:"model"`
	Count int    `json:"count"`
}

// ErrBadQuery: a filter the API cannot take.
var ErrBadQuery = errors.New("bad query")

// List returns one page of source's gems.
func (s *Service) List(ctx context.Context, source string, q Query) (Page, error) {
	if q.Buy != "" && !slices.Contains(buys, q.Buy) {
		return Page{}, fmt.Errorf("%w: buy must be one of %v", ErrBadQuery, buys)
	}
	if !slices.Contains([]string{"", "price", "year"}, q.Sort) {
		return Page{}, fmt.Errorf("%w: sort must be price or year", ErrBadQuery)
	}
	if q.Page < 1 {
		q.Page = 1
	}
	rows, err := s.q.ListGems(ctx, gemsdb.ListGemsParams{
		Source: source, Make: q.Make, Model: q.Model, Buy: q.Buy, MaxPrice: q.MaxPrice,
		MinYear: int32(q.MinYear), MaxMileage: int32(q.MaxMileage), Sort: q.Sort,
		Off: int32((q.Page - 1) * PageSize), Lim: PageSize,
	})
	if err != nil {
		return Page{}, err
	}
	total, err := s.q.CountGems(ctx, gemsdb.CountGemsParams{
		Source: source, Make: q.Make, Model: q.Model, Buy: q.Buy, MaxPrice: q.MaxPrice,
		MinYear: int32(q.MinYear), MaxMileage: int32(q.MaxMileage),
	})
	if err != nil {
		return Page{}, err
	}
	mm, err := s.q.GemMakes(ctx, source)
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: make([]Gem, 0, len(rows)), Total: int(total), Page: q.Page, PageSize: PageSize, Models: make([]MakeModel, 0, len(mm))}
	for _, r := range rows {
		g := Gem{
			ExternalID: r.ExternalID, Title: r.Title, URL: r.Url, ImageURL: r.ImageUrl, Make: r.Make, Model: r.Model,
			Year: int(r.Year), MileageKm: int(r.MileageKm), Fuel: r.Fuel, Price: r.Price, PriceValue: r.PriceValue,
			Buy: r.Buy, Overall: r.Overall,
		}
		_ = json.Unmarshal(r.Fields, &g.Fields)
		p.Items = append(p.Items, g)
	}
	for _, m := range mm {
		p.Models = append(p.Models, MakeModel{Make: m.Make, Model: m.Model, Count: int(m.N)})
	}
	return p, nil
}
