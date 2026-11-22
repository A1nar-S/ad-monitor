package matching

import (
	"context"
	"log/slog"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// peerYears is how many model years either side of a car its price is
// compared with; the nearest of those by year and mileage count most.
const peerYears = 2

// CarPeers reads the stored ads a car is priced against (the listings
// slice, wired in internal/app).
type CarPeers interface {
	CarPeers(ctx context.Context, q PeerQuery) ([]Peer, error)
}

// PeerQuery picks the stored ads a car is priced against.
type PeerQuery struct {
	Source, Make, Model string
	YearMin, YearMax    int
	Exclude             string // the car's own external ID
	Fingerprint         string // its item's; its earlier ads are left out too. "" = none
}

// Peer is a stored ad a car may be priced against. Feed and Location let the
// source say whether it is a car for sale.
type Peer struct {
	Feed, Location string
	Price          float64
	Year           int
	MileageKm      float64  // 0 = unknown
	Fuel           string   // as the source writes it; "" = unknown
	Equipment      []string // from the ad page, as the source names it
}

// WithCarRating rates every alert about a rateable car (see
// sources.CarAds): an overall verdict, its price and equipment against
// similar stored ads, its engine's and gearbox's reputation. Such a car's
// ad page is read for its equipment, unless a WithAdPageFor says otherwise.
func (s *Service) WithCarRating(peers CarPeers) *Service {
	s.peers = peers
	if s.adPageOf == nil {
		s.adPageOf = s.rateableCar
	}
	return s
}

// carAds returns l's source as a car source, if it is one.
func (s *Service) carAds(l events.ListingDiscovered) (sources.CarAds, bool) {
	cars, ok := s.sources.Lookup(l.Source).(sources.CarAds)
	return cars, ok
}

// rateableCar reports whether a listing is a car its source can have rated.
func (s *Service) rateableCar(l events.ListingDiscovered) bool {
	cars, ok := s.carAds(l)
	return ok && cars.RateableCar(l)
}

// dealFields are the verdicts for l, when it is a rateable car. A failed
// lookup costs only the comparisons, never the alert.
func (s *Service) dealFields(ctx context.Context, l events.ListingDiscovered) []events.Field {
	if s.peers == nil {
		return nil
	}
	cars, ok := s.carAds(l)
	if !ok || !cars.RateableCar(l) {
		return nil
	}
	car, ok := deals.CarOf(l, cars.CarTerm)
	if !ok {
		return nil
	}
	rows, err := s.peers.CarPeers(ctx, PeerQuery{
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
