package app

import (
	"context"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems/cars"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/web"
)

// Gem scans stop at 2000 pages (ss.com's cars for sale are about 850), and
// the scraper looks for requests every 15 seconds. Every request to a site
// waits its gap plus up to half again, at random.
const (
	gemMaxPages   = 2000
	gemPollEvery  = 15 * time.Second
	requestJitter = 0.5
)

// gemCategories are what the gem finder rates: the one place a category
// is added.
func gemCategories() []gems.Category { return []gems.Category{cars.Category{}} }

// gemStore lets the gems slice read the listings slice through its own
// types; what a scan sees goes to ingest as events.
type gemStore struct{ *listings.Service }

func (s gemStore) Stored(ctx context.Context, source, location string) ([]gems.Item, error) {
	rows, err := s.StoredIn(ctx, source, location)
	if err != nil {
		return nil, err
	}
	out := make([]gems.Item, len(rows))
	for i, r := range rows {
		out[i] = gems.Item{Listing: r.Listing, Active: r.Active, Detailed: r.Detailed}
	}
	return out, nil
}

// runGemScans runs the scans admins request, spacing feed and ad-page
// requests with the regular scraping's throttles.
func runGemScans(ctx context.Context, svc *gems.Service, store *listings.Service, pub publisher, reg sources.Registry,
	throttles map[string]*scraping.Throttle, detailGap, rerateEvery time.Duration, wg *sync.WaitGroup) {
	scanner := &gems.Scanner{
		Service: svc, Store: gemStore{store}, Keep: pub.listing, KeepAdPage: pub.adPage, Sources: reg,
		Wait: func(ctx context.Context, source string) bool {
			if t, ok := throttles[source]; ok {
				return t.Wait(ctx)
			}
			return ctx.Err() == nil
		},
		DetailGap: detailGap, MaxPages: gemMaxPages, PollEvery: gemPollEvery, RerateEvery: rerateEvery,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner.Run(ctx)
	}()
}

// gemSources maps each source that can be scanned to the categories it
// can be scanned for, for the web.
func gemSources(reg sources.Registry, svc *gems.Service) []web.GemSource {
	var out []web.GemSource
	for _, n := range reg.Names() {
		sc, ok := reg.Lookup(n).(sources.Scannable)
		if !ok {
			continue
		}
		var cats []string
		for _, c := range svc.Categories() {
			if _, ok := sc.ScanScope(c.ID()); ok {
				cats = append(cats, c.ID())
			}
		}
		if len(cats) > 0 {
			out = append(out, web.GemSource{ID: n, Name: n, Categories: cats})
		}
	}
	return out
}
