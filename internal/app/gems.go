package app

import (
	"context"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Gem scans stop at 2000 pages (ss.com's cars for sale are about 850), and
// the scraper looks for requests every 15 seconds. Every request to a site
// waits its gap plus up to half again, at random.
const (
	gemMaxPages   = 2000
	gemPollEvery  = 15 * time.Second
	requestJitter = 0.5
)

// gemStore lets the gems slice use the listings slice through its own types.
type gemStore struct{ *listings.Service }

func (s gemStore) StoredCars(ctx context.Context, source string) ([]gems.Car, error) {
	rows, err := s.Service.StoredCars(ctx, source)
	if err != nil {
		return nil, err
	}
	out := make([]gems.Car, len(rows))
	for i, r := range rows {
		out[i] = gems.Car{Listing: r.Listing, Active: r.Active, Detailed: r.Detailed}
	}
	return out, nil
}

// runGemScans runs the scans admins request, spacing feed and ad-page
// requests with the regular scraping's throttles.
func runGemScans(ctx context.Context, svc *gems.Service, store *listings.Service, reg sources.Registry, throttles map[string]*scraping.Throttle, detailGap time.Duration, wg *sync.WaitGroup) {
	scanner := &gems.Scanner{
		Service: svc, Store: gemStore{store}, Sources: reg,
		Wait: func(ctx context.Context, source string) bool {
			if t, ok := throttles[source]; ok {
				return t.Wait(ctx)
			}
			return ctx.Err() == nil
		},
		DetailGap: detailGap, MaxPages: gemMaxPages, PollEvery: gemPollEvery,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner.Run(ctx)
	}()
}

// gemSource is the source the gem finder scans: the one that can.
func gemSource(reg sources.Registry) string {
	for _, n := range reg.Names() {
		if _, ok := reg[n].(sources.CarScan); ok {
			return n
		}
	}
	return ""
}
