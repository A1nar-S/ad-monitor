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
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// Car is what rating needs from a car ad, in this package's vocabulary.
type Car struct {
	Make, Model string
	Fuel        Fuel    // "" = unknown
	Gearbox     Gearbox // "" = unknown (ss.com: only the ad page has it)
	Body        Body    // "" = unknown (ss.com: only the ad page has it); not rated, for filtering
	// Inspection is whether it has a valid technical inspection (ss.com:
	// only the ad page says), InspectionUntil the month it runs out (zero =
	// not said).
	Inspection      Inspection
	InspectionUntil time.Time
	Year            int
	EngineL         float64 // 0 = unknown
	MileageKm       float64 // 0 = unknown
	Price           float64 // 0 = no price
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

// Inspection is whether a car has a valid technical inspection.
type Inspection string

const (
	Inspected    Inspection = "valid"
	NotInspected Inspection = "none"
)

// Body is a car's body type.
type Body string

// Bodies are the body types, as the gem finder offers them.
var Bodies = []Body{"sedan", "wagon", "hatchback", "suv", "coupe", "convertible", "minivan", "minibus", "pickup"}

// Terms translates one of a source's attr values into this package's
// vocabulary: for "fuel" a Fuel, for "gearbox" a Gearbox, for "body" a Body,
// for "inspection" an Inspection, for "equipment" a feature name. "" = no
// equivalent. A car's "inspection_until" ("2027-02") is read as is.
type Terms func(key, value string) string

// Knows reports whether term is in the vocabulary for key, so a source can
// test its translations.
func Knows(key, term string) bool {
	switch key {
	case "fuel":
		return slices.Contains([]Fuel{Petrol, Diesel, Hybrid, Electric}, Fuel(term))
	case "gearbox":
		return term == string(Automatic) || term == string(Manual)
	case "body":
		return slices.Contains(Bodies, Body(term))
	case "inspection":
		return term == string(Inspected) || term == string(NotInspected)
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
	body, _ := l.Str("body")
	c.Body = Body(terms("body", body))
	inspection, _ := l.Str("inspection")
	c.Inspection = Inspection(terms("inspection", inspection))
	if until, ok := l.Str("inspection_until"); ok {
		c.InspectionUntil, _ = time.Parse("2006-01", until)
	}
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

func (v Verdict) String() string { return enPhrases.verdict[v] }

// The price is judged in euros against a tolerance that grows slower than
// the price: what a gap means depends on what the car costs. 300 € over a
// 1 500 € car is a haggle; 5 000 € over a 20 000 € one is a lot.
//
//	tol = max(0.12 × median, 0.2 × √(15 000 × median))
//	z   = (median − price) / tol    (+ = cheaper than similar ads)
//
// tol is 20% of a 15 000 € median, 63% of 1 500 €, 16% of 22 500 € and
// never below 12%.
const (
	tolRef   = 15000.0
	tolShare = 0.20
	tolFloor = 0.12
)

// tolerance is how far from median a price may be before it counts fully.
func tolerance(median float64) float64 {
	return max(tolFloor*median, tolShare*math.Sqrt(tolRef*median))
}

// verdictFor names a price diff (relative) and z (in tolerances). Far
// below the median is Suspicious only when the gap is large in euros too: a
// 1 500 € car at 900 € is a cheap car, not a scam.
func verdictFor(diff, z float64) Verdict {
	switch {
	case diff <= -0.40 && z >= 1:
		return Suspicious
	case z >= 0.75:
		return Steal
	case z >= 0.25:
		return Good
	case z > -0.4:
		return Fair
	case z > -1:
		return Pricey
	}
	return Overpriced
}

// priceVs rates price against the median of similar ads.
func priceVs(median, price float64) PriceRating {
	r := PriceRating{Median: median, Tol: tolerance(median)}
	r.Diff = price/median - 1
	r.Z = (median - price) / r.Tol
	r.Verdict = verdictFor(r.Diff, r.Z)
	return r
}

// PriceRating compares a car's price with the median of its most similar
// stored ads.
type PriceRating struct {
	Verdict  Verdict
	Diff     float64 // price/Median - 1: -0.14 is 14% below
	Median   float64
	Tol      float64 // tolerance(Median)
	Z        float64 // (Median - price) / Tol: +1 is a full tolerance below
	Peers    int     // how many ads the median is over
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

	prices := make([]float64, len(near))
	for i, p := range near {
		prices[i] = p.Price
	}
	r := priceVs(median(prices), c.Price)
	r.Peers, r.YearMin, r.YearMax, r.Detailed = len(near), near[0].Year, near[0].Year, c.MileageKm > 0
	for _, p := range near {
		r.YearMin, r.YearMax = min(r.YearMin, p.Year), max(r.YearMax, p.Year)
		if p.MileageKm > 0 {
			if r.KmMin == 0 || p.MileageKm < r.KmMin {
				r.KmMin = p.MileageKm
			}
			r.KmMax = max(r.KmMax, p.MileageKm)
		}
	}
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

// Gap is how far the price is from the median, e.g. "1 450 € (14%)".
func (r PriceRating) Gap() string {
	return fmt.Sprintf("%s (%d%%)", Euros(math.Abs(r.Diff*r.Median)), int(math.Round(math.Abs(r.Diff)*100)))
}

// Text is the price line of an alert, e.g. "🔥 Steal: 1 450 € (14%) below
// the median of 12 similar ads (10 350 €; 2015–2018, 140–230k km)".
func (r PriceRating) Text() string { return r.TextIn(En) }

// TextIn is the price line in l.
func (r PriceRating) TextIn(l Lang) string {
	p := l.ph()
	var pos string
	switch {
	case math.Round(math.Abs(r.Diff)*100) < 2:
		pos = fmt.Sprintf(p.atMedian, r.Peers)
	case r.Diff < 0:
		pos = fmt.Sprintf(p.belowMedian, r.Gap(), r.Peers)
	default:
		pos = fmt.Sprintf(p.aboveMedian, r.Gap(), r.Peers)
	}
	scope := strconv.Itoa(r.YearMin)
	if r.YearMax != r.YearMin {
		scope += "–" + strconv.Itoa(r.YearMax)
	}
	if r.KmMax > 0 {
		scope += ", " + l.kmRange(r.KmMin, r.KmMax)
	}
	s := fmt.Sprintf(p.priceLine, p.verdict[r.Verdict], pos, Euros(r.Median), scope)
	switch {
	case r.Verdict == Suspicious:
		s += p.priceSuspicious
	case !r.Detailed:
		s += p.priceYearOnly
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
func FieldsOf(r Ratings) []events.Field { return FieldsIn(r, En) }

// FieldsIn renders ratings already made as the alert lines, in l.
func FieldsIn(r Ratings, l Lang) []events.Field {
	name := l.ph().fields
	var fs []events.Field
	if text, ok := r.TextIn(l); ok {
		fs = append(fs, events.Field{Name: name[0], Value: text})
	}
	if r.Price != nil {
		fs = append(fs, events.Field{Name: name[1], Value: r.Price.TextIn(l)})
	}
	if r.Mileage != nil {
		fs = append(fs, events.Field{Name: name[2], Value: r.Mileage.TextIn(l)})
	}
	if r.Inspection != nil {
		fs = append(fs, events.Field{Name: name[3], Value: r.Inspection.TextIn(l)})
	}
	if r.Engine != nil {
		fs = append(fs, events.Field{Name: name[4], Value: r.Engine.TextIn(l)})
	}
	if r.Gearbox != nil {
		fs = append(fs, events.Field{Name: name[5], Value: r.Gearbox.TextIn(l)})
	}
	if r.Equipment != nil {
		fs = append(fs, events.Field{Name: name[6], Value: r.Equipment.TextIn(l)})
	}
	return fs
}
