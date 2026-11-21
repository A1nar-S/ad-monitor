// Package deals rates a car ad for someone who does not know cars: is it a
// good buy overall, is the price low or high for what it is, is it well
// equipped, and is its engine (and gearbox) known as good or troublesome.
//
// It is plain logic in one English vocabulary (Fuel, Gearbox, the feature
// names in equipment.go). Sources translate their own words into it
// (sources.CarAds.CarTerm), so no site's language appears here; the caller
// fetches the stored ads to compare with.
package deals

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// Car is what rating needs from a car ad, in this package's vocabulary.
type Car struct {
	Make, Model string
	Fuel        Fuel    // "" = unknown
	Gearbox     Gearbox // "" = unknown (ss.com: only the ad page has it)
	Year        int
	EngineL     float64 // 0 = unknown
	MileageKm   float64 // 0 = unknown
	Price       float64 // 0 = no price
	// Equipment are feature names (see features); none = unknown or not
	// ticked (ss.com: only the ad page has it).
	Equipment []string
}

// Fuel is a car's fuel.
type Fuel string

const (
	Petrol   Fuel = "petrol" // petrol/LPG too: list pages cannot tell them apart
	Diesel   Fuel = "diesel"
	Hybrid   Fuel = "hybrid"
	Electric Fuel = "electric"
)

// Gearbox is a car's gearbox.
type Gearbox string

const (
	Automatic Gearbox = "automatic"
	Manual    Gearbox = "manual"
)

// Terms translates one of a source's attr values into this package's
// vocabulary: for "fuel" a Fuel, for "gearbox" a Gearbox, for "equipment" a
// feature name. "" = no equivalent.
type Terms func(key, value string) string

// Knows reports whether term is in the vocabulary for key, so a source can
// test its translations.
func Knows(key, term string) bool {
	switch key {
	case "fuel":
		return slices.Contains([]Fuel{Petrol, Diesel, Hybrid, Electric}, Fuel(term))
	case "gearbox":
		return term == string(Automatic) || term == string(Manual)
	case "equipment":
		_, ok := features[term]
		return ok
	}
	return false
}

// CarOf reads a car from a listing's attrs, translating its fuel, gearbox and
// equipment through terms. ok is false without a make, a model and a year.
func CarOf(l events.ListingDiscovered, terms Terms) (Car, bool) {
	var c Car
	c.Make, _ = l.Str("make")
	c.Model, _ = l.Str("model")
	year, _ := l.Num("year")
	c.Year = int(year)
	if c.Make == "" || c.Model == "" || c.Year == 0 {
		return Car{}, false
	}
	fuel, _ := l.Str("fuel")
	c.Fuel = Fuel(terms("fuel", fuel))
	gearbox, _ := l.Str("gearbox")
	c.Gearbox = Gearbox(terms("gearbox", gearbox))
	c.EngineL, _ = l.Num("engine_l")
	c.MileageKm, _ = l.Num("mileage_km")
	c.Equipment = Translate(terms, "equipment", Strings(l.Attrs["equipment"]))
	if l.PriceValue != nil {
		c.Price = *l.PriceValue
	}
	return c, true
}

// Translate maps values of key through terms, dropping those without an
// equivalent.
func Translate(terms Terms, key string, values []string) []string {
	var out []string
	for _, v := range values {
		if t := terms(key, v); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Strings reads a list attr: []any of strings after a JSON round trip.
func Strings(v any) []string {
	var out []string
	switch v := v.(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Peer is a stored ad of the same make and model, to compare a price and
// equipment with.
type Peer struct {
	Price     float64
	Year      int
	MileageKm float64  // 0 = unknown
	Fuel      Fuel     // "" = unknown
	Equipment []string // feature names; none = unknown (its ad page was never read) or not ticked
}

const (
	// MinPeers: with fewer similar ads a verdict would be noise dressed up
	// as advice, so there is none.
	MinPeers = 5
	// nearest is how many of the most similar ads the median is taken over.
	nearest = 15
	// junkPrice: below this an ad's price is a placeholder ("1 €", "call"),
	// not a price.
	junkPrice = 300.0
	// kmPerYear weighs mileage against age when picking similar ads: 40 000
	// km apart counts like one model year apart.
	kmPerYear = 40000.0
	// unknownKm is the distance charged when either mileage is unknown.
	unknownKm = 0.5
)

// Verdict is how a price compares with similar ads.
type Verdict int

const (
	Suspicious Verdict = iota // far below: damage, a scam or a typo is likelier than luck
	Steal
	Good
	Fair
	Pricey
	Overpriced
)

func (v Verdict) String() string {
	return [...]string{"🚩 Suspiciously cheap", "🔥 Steal", "👍 Good price", "😐 Fair", "👎 Pricey", "💩 Overpriced"}[v]
}

func verdictFor(diff float64) Verdict {
	switch {
	case diff <= -0.40:
		return Suspicious
	case diff <= -0.15:
		return Steal
	case diff <= -0.05:
		return Good
	case diff < 0.08:
		return Fair
	case diff < 0.20:
		return Pricey
	}
	return Overpriced
}

// PriceRating compares a car's price with the median of its most similar
// stored ads.
type PriceRating struct {
	Verdict  Verdict
	Diff     float64 // price/Median - 1: -0.14 is 14% below
	Median   float64
	Peers    int // how many ads the median is over
	YearMin  int
	YearMax  int
	KmMin    float64 // both 0 when no compared ad has a mileage
	KmMax    float64
	Detailed bool // mileage was part of the comparison
}

// RatePrice rates c's price against peers: the nearest ones by year and
// mileage, at most 15, at least MinPeers. ok is false when there are too few
// or c has no real price.
func RatePrice(c Car, peers []Peer) (PriceRating, bool) {
	if c.Price < junkPrice {
		return PriceRating{}, false
	}
	type scored struct {
		Peer
		dist float64
	}
	var near []scored
	for _, p := range peers {
		if p.Price < junkPrice || c.Fuel != "" && p.Fuel != c.Fuel {
			continue
		}
		d := math.Abs(float64(p.Year - c.Year))
		if c.MileageKm > 0 && p.MileageKm > 0 {
			d += math.Abs(p.MileageKm-c.MileageKm) / kmPerYear
		} else {
			d += unknownKm
		}
		near = append(near, scored{p, d})
	}
	if len(near) < MinPeers {
		return PriceRating{}, false
	}
	slices.SortStableFunc(near, func(a, b scored) int { return cmp.Compare(a.dist, b.dist) })
	near = near[:min(nearest, len(near))]

	r := PriceRating{Peers: len(near), YearMin: near[0].Year, YearMax: near[0].Year, Detailed: c.MileageKm > 0}
	prices := make([]float64, len(near))
	for i, p := range near {
		prices[i] = p.Price
		r.YearMin, r.YearMax = min(r.YearMin, p.Year), max(r.YearMax, p.Year)
		if p.MileageKm > 0 {
			if r.KmMin == 0 || p.MileageKm < r.KmMin {
				r.KmMin = p.MileageKm
			}
			r.KmMax = max(r.KmMax, p.MileageKm)
		}
	}
	r.Median = median(prices)
	r.Diff = c.Price/r.Median - 1
	r.Verdict = verdictFor(r.Diff)
	return r, true
}

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Text is the price line of an alert, e.g. "🔥 Steal: 14% below the median
// of 12 similar ads (10 350 €; 2015–2018, 140–230k km)".
func (r PriceRating) Text() string {
	var pos string
	switch pct := int(math.Round(math.Abs(r.Diff) * 100)); {
	case pct < 2:
		pos = "about the median"
	case r.Diff < 0:
		pos = fmt.Sprintf("%d%% below the median", pct)
	default:
		pos = fmt.Sprintf("%d%% above the median", pct)
	}
	years := strconv.Itoa(r.YearMin)
	if r.YearMax != r.YearMin {
		years += "–" + strconv.Itoa(r.YearMax)
	}
	scope := years
	if r.KmMax > 0 {
		km := thousands(r.KmMin)
		if hi := thousands(r.KmMax); hi != km {
			km += "–" + hi
		}
		scope += ", " + km + "k km"
	}
	s := fmt.Sprintf("%s: %s of %d similar ads (%s; %s)", r.Verdict, pos, r.Peers, Euros(r.Median), scope)
	switch {
	case r.Verdict == Suspicious:
		s += ". Check for damage, a scam or a price typo."
	case !r.Detailed:
		s += ". Mileage unknown, so compared on year only."
	}
	return s
}

func thousands(km float64) string { return strconv.Itoa(int(math.Round(km / 1000))) }

// Euros formats a price the way ss.com does: "10 350 €".
func Euros(v float64) string {
	digits := strconv.Itoa(int(math.Round(v)))
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(d)
	}
	return b.String() + " €"
}

// Fields are the alert lines for c, each only when there is something to
// say: the overall verdict first, then what it rests on.
func Fields(c Car, peers []Peer) []events.Field { return FieldsOf(Rate(c, peers)) }

// FieldsOf renders ratings already made as the alert lines.
func FieldsOf(r Ratings) []events.Field {
	var fs []events.Field
	if text, ok := r.Text(); ok {
		fs = append(fs, events.Field{Name: "⭐ Overall", Value: text})
	}
	if r.Price != nil {
		fs = append(fs, events.Field{Name: "🏷️ Price verdict", Value: r.Price.Text()})
	}
	if r.Engine != nil {
		fs = append(fs, events.Field{Name: "🔧 Engine verdict", Value: r.Engine.Text()})
	}
	if r.Gearbox != nil {
		fs = append(fs, events.Field{Name: "🕹️ Gearbox verdict", Value: r.Gearbox.Text()})
	}
	if r.Equipment != nil {
		fs = append(fs, events.Field{Name: "🧰 Equipment verdict", Value: r.Equipment.Text()})
	}
	return fs
}
