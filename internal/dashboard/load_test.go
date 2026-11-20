package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/notifications"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// Runs against a real Postgres when TEST_DATABASE_URL is set; the database
// must be empty and disposable.
func TestLoadPagesInSQL(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, m := range []func(context.Context) error{
		listings.NewService(pool, time.Hour).Migrate,
		subscriptions.NewService(pool, nil, 0).Migrate,
		notifications.NewService(pool, 0).Migrate,
	} {
		if err := m(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "TRUNCATE listings CASCADE"); err != nil {
		t.Fatal(err)
	}

	// 45 cars (every third gone), 5 flats, 1 from another source.
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	insert := func(source, id, title, location string, seen time.Time, active bool) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO listings (source, external_id, title, url, location, first_seen_at, is_active)
			VALUES ($1, $2, $3, 'https://example.com/' || $2, $4, $5, $6)`, source, id, title, location, seen, active)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 45 {
		insert("ss.com", fmt.Sprintf("car%d", i), fmt.Sprintf("Audi A%d", i), "transport/cars/audi", start.Add(time.Duration(i)*time.Minute), i%3 != 0)
	}
	for i := range 5 {
		insert("ss.com", fmt.Sprintf("flat%d", i), "Flat 50%_off", "real-estate/flats/riga/centre", start.Add(time.Hour+time.Duration(i)*time.Minute), true)
	}
	insert("other", "x1", "Zebra", "", start.Add(2*time.Hour), true)

	load := Load(pool)
	get := func(q Query) (out struct {
		Sources []string `json:"sources"`
		Total   int      `json:"total"`
		Page    int      `json:"page"`
		Pages   int      `json:"pages"`
		Items   []struct {
			Title     string  `json:"title"`
			Active    bool    `json:"active"`
			Category  *string `json:"category"`
			HasAdPage bool    `json:"hasAdPage"`
		} `json:"items"`
	}) {
		t.Helper()
		if q.View == "" {
			q.View = "listings"
		}
		if q.Sort == "" {
			q.Sort = "newest"
		}
		if q.Page == 0 {
			q.Page = 1
		}
		raw, err := load(ctx, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	all := get(Query{})
	if all.Total != 51 || all.Pages != 3 || len(all.Items) != PageSize || all.Items[0].Title != "Zebra" {
		t.Fatalf("all: total %d pages %d items %d first %q", all.Total, all.Pages, len(all.Items), all.Items[0].Title)
	}
	if fmt.Sprint(all.Sources) != "[other ss.com]" {
		t.Errorf("sources = %v", all.Sources)
	}
	if all.Items[0].Category != nil || all.Items[0].HasAdPage {
		t.Errorf("other source got category/ad page: %+v", all.Items[0])
	}

	last := get(Query{Page: 99})
	if last.Page != 3 || len(last.Items) != 11 {
		t.Errorf("past the end: page %d items %d, want clamped to 3 with 11", last.Page, len(last.Items))
	}

	cars := get(Query{Category: "cars", Status: "inactive", Sort: "oldest"})
	if cars.Total != 15 || cars.Items[0].Title != "Audi A0" || cars.Items[0].Active || !cars.Items[0].HasAdPage || *cars.Items[0].Category != "cars" {
		t.Errorf("inactive cars: total %d first %+v", cars.Total, cars.Items[0])
	}

	// Wildcards in the search box match literally.
	if n := get(Query{Text: "50%_OFF"}).Total; n != 5 {
		t.Errorf("literal %%_ search = %d, want 5", n)
	}
	if n := get(Query{Text: "%"}).Total; n != 5 {
		t.Errorf("%% alone = %d, want only the 5 titles containing it", n)
	}

	titles := get(Query{Sort: "title", Source: "ss.com", Category: "flats"})
	if titles.Total != 5 {
		t.Errorf("flats = %d", titles.Total)
	}

	if s := get(Query{View: "searches", Text: "anyone", Status: "active"}); s.Total != 0 || s.Items == nil {
		t.Errorf("searches = %+v", s)
	}
}
