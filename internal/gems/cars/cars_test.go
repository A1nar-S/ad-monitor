package cars

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/deals"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
)

// site is a car source already writing internal/deals' words.
type site struct{}

func (site) Name() string { return "ss.com" }
func (site) FetchFeed(context.Context, string, int) ([]events.ListingDiscovered, error) {
	return nil, nil
}
func (site) PlanFeeds(w []string, _ int) []string            { return w }
func (site) InScope([]string, events.ListingDiscovered) bool { return true }
func (site) CarTerm(key, v string) string                    { return map[bool]string{true: v}[deals.Knows(key, v)] }
func (site) RateableCar(l events.ListingDiscovered) bool {
	return strings.HasSuffix(l.Feed, "/sell") && strings.HasPrefix(l.Location, "cars/")
}

// parts is a source that lists no whole cars.
type parts struct{}

func (parts) Name() string { return "rrr.lt" }
func (parts) FetchFeed(context.Context, string, int) ([]events.ListingDiscovered, error) {
	return nil, nil
}
func (parts) PlanFeeds(w []string, _ int) []string            { return w }
func (parts) InScope([]string, events.ListingDiscovered) bool { return true }

func bmw(id string, price float64) gems.Item {
	return gems.Item{Active: true, Listing: events.ListingDiscovered{
		Source: "ss.com", ExternalID: id, Title: "BMW 520d", Feed: "cars/sell", Location: "cars/bmw/520",
		Price: "€", PriceValue: &price,
		Attrs: map[string]any{"make": "BMW", "model": "520", "year": 2016.0, "engine_l": 2.0, "fuel": "diesel", "mileage_km": 180000.0},
	}}
}

// detailed is what its ad page adds to a car.
func detailed(it gems.Item) gems.Item {
	l := it.Listing
	l.Attrs = map[string]any{"body": "wagon", "region": "Rīga", "gearbox": "automatic", "inspection": "valid",
		"inspection_until": "2030-05", "equipment": []any{"climate control", "adaptive cruise control", "reversing camera",
			"xenon lights", "keyless entry", "front parking sensors", "360° cameras", "sunroof", "lane assist", "sport package"}}
	for k, v := range it.Listing.Attrs {
		l.Attrs[k] = v
	}
	it.Listing, it.Detailed = l, true
	return it
}

func ids(items []gems.Item, rs []gems.Rated) []string {
	var out []string
	for _, x := range rs {
		out = append(out, items[x.Item].Listing.ExternalID)
	}
	return out
}

func TestRater(t *testing.T) {
	lada := gems.Item{Active: true, Listing: events.ListingDiscovered{Source: "ss.com", ExternalID: "lada", Feed: "cars/sell",
		Location: "cars/lada/niva", PriceValue: new(float64), Attrs: map[string]any{"make": "Lada", "model": "Niva", "year": 2010.0}}}
	*lada.Listing.PriceValue = 2000
	sold := bmw("sold", 9900)
	sold.Active = false
	wanted := bmw("wanted", 9000)
	wanted.Listing.Feed = "cars/buy"
	items := []gems.Item{
		bmw("cheap", 7000), // 30% below its peers: a steal
		bmw("p1", 10000), bmw("p2", 10200), bmw("p3", 9800), bmw("p4", 10000), bmw("p5", 10100),
		bmw("dear", 12800), lada, sold, wanted,
	}
	r, ok := Category{}.Rater(site{}, items)
	if !ok {
		t.Fatal("a car source cannot be rated")
	}
	if _, ok := (Category{}).Rater(parts{}, items); ok {
		t.Error("a source without whole cars can be rated")
	}

	rs := r.Rate()
	got := ids(items, rs)
	// Not the Niva (nothing to compare with), the sold one, nor a wanted ad;
	// the sold one still counts as a peer.
	if len(rs) != 7 || got[0] != "cheap" || slices.Contains(got, "lada") || slices.Contains(got, "sold") || slices.Contains(got, "wanted") {
		t.Fatalf("rated %v, want the 7 BMWs for sale, cheap first", got)
	}
	var candidates []string
	for _, x := range rs {
		if x.Candidate {
			candidates = append(candidates, items[x.Item].Listing.ExternalID)
		}
	}
	if len(candidates) != 6 || slices.Contains(candidates, "dear") {
		t.Errorf("candidates = %v, want the 6 fairly priced, not the overpriced one", candidates)
	}
	best := rs[0]
	if best.Heading != "BMW 520" || best.Buy != "good" || best.Display <= 50 || best.PriceDiff == nil || *best.PriceDiff > -0.25 {
		t.Errorf("best = %+v", best)
	}
	a := best.Attrs
	if a["make"] != "BMW" || a["model"] != "520" || a["year"] != 2016.0 || a["mileage_km"] != 180000.0 || a["fuel"] != "diesel" {
		t.Errorf("list-page attrs = %v", a)
	}
	if _, ok := a["body"]; ok {
		t.Errorf("a body before the ad page was read: %v", a)
	}

	// Its ad page read: the details are there and it is rated again.
	items[0] = detailed(items[0])
	r.Update(0, items[0])
	rs = r.Rate()
	best = rs[0]
	a = best.Attrs
	if items[best.Item].Listing.ExternalID != "cheap" || a["body"] != "wagon" || a["region"] != "Rīga" ||
		a["gearbox"] != "automatic" || a["inspection"] != "valid" {
		t.Errorf("after its ad page: %v", a)
	}
	var keys []string
	for _, c := range best.Components {
		keys = append(keys, c.Key)
	}
	if !slices.Contains(keys, "inspection") || !slices.Contains(keys, "gearbox") || !slices.Contains(keys, "equipment") {
		t.Errorf("components %v lack what the ad page added", keys)
	}
	var names []string
	for _, f := range best.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, "🧰 Equipment verdict") || !slices.Contains(names, "📋 Inspection verdict") {
		t.Errorf("verdicts %v lack the ad page's", names)
	}

	// Removed: no longer rated.
	items[0].Active = false
	r.Update(0, items[0])
	if got := ids(items, r.Rate()); slices.Contains(got, "cheap") {
		t.Errorf("a removed car is still rated: %v", got)
	}
}

// Every attr a rated car carries is one of the Spec's fields, so it can be
// filtered on and shown.
func TestAttrsMatchSpec(t *testing.T) {
	spec := Category{}.Spec()
	items := []gems.Item{detailed(bmw("a", 7000))}
	for _, id := range []string{"b", "c", "d", "e", "f"} {
		items = append(items, bmw(id, 10000))
	}
	r, _ := Category{}.Rater(site{}, items)
	for _, x := range r.Rate() {
		for k := range x.Attrs {
			if !slices.ContainsFunc(spec.Fields, func(f gems.Field) bool { return f.Key == k }) {
				t.Errorf("attr %q is not in the spec", k)
			}
		}
	}
	for _, s := range spec.Sorts {
		if !slices.ContainsFunc(spec.Fields, func(f gems.Field) bool { return f.Key == s.Key && f.Kind == gems.Range }) {
			t.Errorf("sort %q is not by a numeric field", s.ID)
		}
	}
}

func TestBelongs(t *testing.T) {
	c := Category{}
	if !c.Belongs(site{}, events.ListingDiscovered{Feed: "cars/sell", Location: "cars/bmw/520"}) {
		t.Error("a car for sale does not belong")
	}
	if c.Belongs(site{}, events.ListingDiscovered{Feed: "cars/buy", Location: "cars/bmw/520"}) {
		t.Error("a wanted ad belongs")
	}
	if c.Belongs(parts{}, events.ListingDiscovered{Feed: "cars/sell", Location: "cars/bmw/520"}) {
		t.Error("a part belongs")
	}
}
