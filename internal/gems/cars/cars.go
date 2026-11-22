// Package cars is the gem finder's car category: every car for sale rated
// by internal/deals against the stored ads like it (price, mileage for its
// age, inspection, engine, gearbox, equipment). A source takes part by
// implementing sources.CarAds (and sources.Scannable for "cars").
package cars

import (
	"cmp"
	"slices"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// Category implements gems.Category for cars.
type Category struct{}

var _ gems.Category = Category{}

// ID is the sources' category ID.
func (Category) ID() string { return "cars" }

// Spec: the attrs a rated car carries (Rated.Attrs), as filters and on the
// cards. Fuel, gearbox and body are in internal/deals' words; region is the
// site's.
func (Category) Spec() gems.Spec {
	return gems.Spec{
		Fields: []gems.Field{
			{Key: "make", Kind: gems.Choice, Control: "select"},
			{Key: "model", Kind: gems.Choice, Control: "select", DependsOn: "make"},
			{Key: "year", Kind: gems.Range, Control: "range", Card: true},
			{Key: "mileage_km", Kind: gems.Range, Control: "max", Unit: "km", Card: true},
			{Key: "engine_l", Kind: gems.Range, Control: "range", Unit: "l", Card: true},
			{Key: "fuel", Kind: gems.Choice, Control: "chips", Card: true},
			{Key: "gearbox", Kind: gems.Choice, Control: "chips", Detail: true, Card: true},
			{Key: "body", Kind: gems.Choice, Control: "chips", Detail: true, Card: true},
			{Key: "inspection", Kind: gems.Choice, Control: "chips", Detail: true, Card: true,
				Tones: map[string]string{string(deals.Inspected): "good", string(deals.NotInspected): "bad"}},
			{Key: "region", Kind: gems.Choice, Control: "search", Detail: true, Card: true, Icon: "pin", Catalog: true},
		},
		Sorts: []gems.Sort{
			{ID: "year", Key: "year", Desc: true},
			{ID: "mileage", Key: "mileage_km"},
		},
	}
}

// Belongs reports whether l is a car for sale src can rate.
func (Category) Belongs(src sources.Source, l events.ListingDiscovered) bool {
	ads, ok := src.(sources.CarAds)
	return ok && ads.RateableCar(l)
}

// Rater rates every active car for sale among items against all of them:
// sold ones still count, their asking prices are the market.
func (Category) Rater(src sources.Source, items []gems.Item) (gems.Rater, bool) {
	ads, ok := src.(sources.CarAds)
	if !ok {
		return nil, false
	}
	r := &rater{byModel: map[string][]int{}, terms: ads.CarTerm, isCar: ads.RateableCar}
	for _, it := range items {
		r.cars = append(r.cars, car{item: it})
		i := len(r.cars) - 1
		r.read(i)
		if r.cars[i].ok {
			k := modelKey(r.cars[i].car)
			r.byModel[k] = append(r.byModel[k], i)
		}
	}
	return r, true
}

// peerYears: similar cars are within this many model years.
const peerYears = 2

type rater struct {
	cars    []car
	byModel map[string][]int // make|model, lower-cased -> indexes into cars
	terms   deals.Terms
	isCar   func(events.ListingDiscovered) bool
}

type car struct {
	item gems.Item
	car  deals.Car
	peer deals.Peer
	ok   bool // a car for sale with a make, model, year and price
}

func modelKey(c deals.Car) string { return strings.ToLower(c.Make + "|" + c.Model) }

// read (re)reads car i from its listing. Its make and model never change,
// so its place in byModel holds.
func (r *rater) read(i int) {
	c := &r.cars[i]
	l := c.item.Listing
	dc, ok := deals.CarOf(l, r.terms)
	c.car, c.ok = dc, ok && r.isCar(l) && l.PriceValue != nil
	if c.ok {
		c.peer = deals.Peer{Price: dc.Price, Year: dc.Year, MileageKm: dc.MileageKm, Fuel: dc.Fuel, Equipment: dc.Equipment}
	}
}

func (r *rater) Update(i int, it gems.Item) {
	r.cars[i].item = it
	r.read(i)
}

// Rate rates every active car with enough to go on, best first.
func (r *rater) Rate() []gems.Rated {
	var out []gems.Rated
	for i, c := range r.cars {
		if !c.ok || !c.item.Active {
			continue
		}
		if x, ok := r.rate(i); ok {
			out = append(out, x)
		}
	}
	slices.SortStableFunc(out, func(a, b gems.Rated) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score),
			cmp.Compare(r.cars[a.Item].item.Listing.ExternalID, r.cars[b.Item].item.Listing.ExternalID))
	})
	return out
}

var buyNames = map[deals.Buy]string{deals.GoodBuy: "good", deals.AverageBuy: "average", deals.BadBuy: "bad", deals.CheckFirst: "check"}

func (r *rater) rate(i int) (gems.Rated, bool) {
	c := r.cars[i]
	var peers []deals.Peer
	for _, j := range r.byModel[modelKey(c.car)] {
		p := r.cars[j]
		if j == i || !p.ok || p.car.Year < c.car.Year-peerYears || p.car.Year > c.car.Year+peerYears {
			continue
		}
		if fp := c.item.Listing.Fingerprint; fp != "" && p.item.Listing.Fingerprint == fp {
			continue // its own earlier ad
		}
		peers = append(peers, p.peer)
	}
	ratings := deals.Rate(c.car, peers)
	if ratings.Price == nil && ratings.Engine == nil {
		return gems.Rated{}, false
	}
	overall, ok := ratings.Text()
	if !ok {
		return gems.Rated{}, false
	}
	b, _, _ := ratings.Overall()
	score := ratings.Score()
	x := gems.Rated{
		Item: i, Score: score, Display: deals.Display(score), Buy: buyNames[b],
		Heading: c.car.Make + " " + c.car.Model, Overall: overall, Fields: deals.FieldsOf(ratings),
		Candidate: deals.Candidate(ratings), Attrs: attrsOf(c.car, c.item.Listing, ratings),
	}
	for _, p := range ratings.Components() {
		x.Components = append(x.Components, gems.Component{
			Key: p.Key, X: p.X, Weight: p.Weight, Points: p.Points, Score: deals.DisplayPoints(p.Points), Why: p.Why,
		})
	}
	if ratings.Price != nil {
		d := ratings.Price.Diff
		x.PriceDiff = &d
	}
	return x, true
}

// attrsOf are the Spec's values for a car; unknown ones are left out.
func attrsOf(c deals.Car, l events.ListingDiscovered, r deals.Ratings) map[string]any {
	a := map[string]any{"make": c.Make, "model": c.Model, "year": float64(c.Year)}
	num := func(k string, v float64) {
		if v > 0 {
			a[k] = v
		}
	}
	str := func(k, v string) {
		if v != "" {
			a[k] = v
		}
	}
	num("mileage_km", c.MileageKm)
	num("engine_l", c.EngineL)
	str("fuel", string(c.Fuel))
	str("gearbox", string(c.Gearbox))
	str("body", string(c.Body))
	if i := r.Inspection; i != nil {
		// One that ran out counts as none.
		a["inspection"] = string(map[bool]deals.Inspection{true: deals.Inspected, false: deals.NotInspected}[i.Valid])
	}
	region, _ := l.Str("region")
	str("region", region)
	return a
}
