package app

import (
	"context"
	"log/slog"
	"slices"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// peerYears is how many model years either side of a car its price is
// compared with; the nearest of those by year and mileage count most.
const peerYears = 2

// addDealFields appends the deal verdicts to every alert about one rateable
// car: an overall verdict, its price and equipment against similar stored
// ads, and its engine's and gearbox's reputation. ns all carry the same
// listing (read once, with its ad page when that was read). Anything else
// gets nothing, and a failed lookup costs only the comparisons, never the
// alert.
func addDealFields(ctx context.Context, store *listings.Service, reg sources.Registry, ns []subscriptions.Notification) {
	if len(ns) == 0 || ns[0].Listing == nil {
		return
	}
	extra := dealFields(ctx, store, reg, *ns[0].Listing)
	if len(extra) == 0 {
		return
	}
	for i := range ns {
		ns[i].Listing.Fields = slices.Concat(ns[i].Listing.Fields, extra)
	}
}

// carAds returns l's source as a car source, if it is one.
func carAds(reg sources.Registry, l events.ListingDiscovered) (sources.CarAds, bool) {
	src, err := reg.Get(l.Source)
	if err != nil {
		return nil, false
	}
	cars, ok := src.(sources.CarAds)
	return cars, ok
}

// rateableCar reports whether a listing is a car its source can have rated.
func rateableCar(reg sources.Registry) func(events.ListingDiscovered) bool {
	return func(l events.ListingDiscovered) bool {
		cars, ok := carAds(reg, l)
		return ok && cars.RateableCar(l)
	}
}

func dealFields(ctx context.Context, store *listings.Service, reg sources.Registry, l events.ListingDiscovered) []events.Field {
	cars, ok := carAds(reg, l)
	if !ok || !cars.RateableCar(l) {
		return nil
	}
	car, ok := deals.CarOf(l, cars.CarTerm)
	if !ok {
		return nil
	}
	rows, err := store.CarPeers(ctx, listings.PeerQuery{
		Source: l.Source, Make: car.Make, Model: car.Model,
		YearMin: car.Year - peerYears, YearMax: car.Year + peerYears, Exclude: l.ExternalID, Fingerprint: l.Fingerprint,
	})
	if err != nil {
		slog.Warn("could not read similar ads to rate a car", "listing", l.ExternalID, "err", err)
	}
	var peers []deals.Peer
	for _, r := range rows {
		if cars.RateableCar(events.ListingDiscovered{Source: l.Source, Feed: r.Feed, Location: r.Location}) {
			peers = append(peers, deals.Peer{
				Price: r.Price, Year: r.Year, MileageKm: r.MileageKm,
				Fuel:      deals.Fuel(cars.CarTerm("fuel", r.Fuel)),
				Equipment: deals.Translate(cars.CarTerm, "equipment", r.Equipment),
			})
		}
	}
	return deals.Fields(car, peers)
}
