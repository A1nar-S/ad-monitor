package deals

import (
	"fmt"
	"math"
)

// The score is a weighted sum: each thing known about a car gives x from
// −1 to +1 (unknown = 0) and S = Σ weight × x.
//
//	price       3    clamp(z, −1, 1): a tolerance below similar ads is +1 (deals.go)
//	mileage     2    RateMileage: against what is usual for its age
//	inspection  2    RateInspection: none −1, runs out within 3 months −0.25, valid 0
//	engine      2    good +1, ok 0, depends −0.25, risky −1 × wear
//	gearbox     1.5  as the engine, without wear
//	reliability 1.5  RateReliability: how often the model fails inspection at the first attempt (failrate.go)
//	equipment   1.5  against similar ads 2 × beaten share − 1; else well +1, basic −1
//
// wear = clamp(km / 200 000, 0.5, 1.5) (1 with no mileage): a risky engine
// that has done many km is closer to its failure. S ≥ 4 is a good buy (a
// fair price and a durable engine and gearbox are not enough: it needs a
// price below similar ads or good equipment too), S ≤ −2 a bad one; at
// least two parts must be known.
const (
	weightPrice     = 3.0
	weightMileage   = 2.0
	weightInspect   = 2.0
	weightEngine    = 2.0
	weightGearbox   = 1.5
	weightReliable  = 1.5
	weightEquipment = 1.5

	wearKm = 200000.0

	goodScore = 4.0
	badScore  = -2.0
	minKnown  = 2
)

// Component is one thing's part in the score.
type Component struct {
	Key    string  `json:"key"` // price, mileage, inspection, engine, gearbox, reliability, equipment
	X      float64 `json:"x"`   // −1..+1 (a worn risky engine down to −1.5)
	Weight float64 `json:"weight"`
	Points float64 `json:"points"` // Weight × X
	Why    string  `json:"why"`    // the reason, for the overall line; "" = nothing to say
}

// Components are the score's parts that are known, in a fixed order.
func (r Ratings) Components() []Component { return r.ComponentsIn(En) }

// ComponentsIn are the score's parts, the reasons in l.
func (r Ratings) ComponentsIn(l Lang) []Component {
	ph := l.ph()
	var cs []Component
	add := func(key string, w, x float64, why string) {
		cs = append(cs, Component{Key: key, X: x, Weight: w, Points: w * x, Why: why})
	}
	if p := r.Price; p != nil {
		why := ph.whyFairPrice
		switch {
		case p.Verdict == Suspicious:
			why = fmt.Sprintf(ph.whySuspicious, p.Gap())
		case p.Verdict <= Good:
			why = fmt.Sprintf(ph.whyBelow, p.Gap())
		case p.Verdict >= Pricey:
			why = fmt.Sprintf(ph.whyAbove, p.Gap())
		}
		add("price", weightPrice, clamp(p.Z, -1, 1), why)
	}
	if m := r.Mileage; m != nil {
		why := ""
		switch {
		case m.X >= 0.4:
			why = ph.whyLowKm
		case m.X <= -0.7:
			why = ph.whyVeryHighKm
		case m.X <= -0.3:
			why = ph.whyHighKm
		}
		add("mileage", weightMileage, m.X, why)
	}
	if i := r.Inspection; i != nil {
		why := ""
		switch {
		case !i.Valid:
			why = ph.whyNoInspection
		case i.X < 0:
			why = ph.whyInspectionSoon
		}
		add("inspection", weightInspect, i.X, why)
	}
	wear := 1.0
	if r.Mileage != nil {
		wear = clamp(r.Mileage.Km/wearKm, 0.5, 1.5)
	}
	// what: 0 for the engine, 1 for the gearbox.
	part := func(key string, n *Note, w float64, what int, worn float64) {
		if n == nil {
			return
		}
		switch n.Rating {
		case RatingGood:
			add(key, w, 1, fmt.Sprintf(ph.whyDurable[what], n.NameIn(l)))
		case RatingRisky:
			add(key, w, -worn, fmt.Sprintf(ph.whyRisky[what], n.NameIn(l)))
		case RatingMixed:
			add(key, w, -0.25, ph.whyDepends[what])
		default:
			add(key, w, 0, "")
		}
	}
	part("engine", r.Engine, weightEngine, 0, wear)
	part("gearbox", r.Gearbox, weightGearbox, 1, 1)
	if m := r.Reliability; m != nil {
		why := ""
		switch m.level() {
		case 0:
			why = ph.whyOftenFails
		case 2:
			why = ph.whyRarelyFails
		}
		add("reliability", weightReliable, m.X, why)
	}
	if e := r.Equipment; e != nil {
		x := map[Level]float64{Basic: -1, Average: 0, WellEquipped: 1}[e.Level]
		if e.Trim == "" && e.Of > 0 {
			x = 2*float64(e.Better)/float64(e.Of) - 1
		}
		why := ""
		switch e.Level {
		case WellEquipped:
			why = ph.whyWellEquipped
		case Basic:
			why = ph.whyBasic
		}
		add("equipment", weightEquipment, x, why)
	}
	return cs
}

// judge sums the components: S, the reasons in order (in l), and whether
// enough is known to say anything.
func (r Ratings) judge(l Lang) (float64, []string, bool) {
	cs := r.ComponentsIn(l)
	if len(cs) < minKnown {
		return 0, nil, false
	}
	if r.Price != nil && r.Price.Verdict == Suspicious {
		return 0, []string{cs[0].Why}, true
	}
	var s float64
	var why []string
	for _, c := range cs {
		s += c.Points
		if c.Why != "" {
			why = append(why, c.Why)
		}
	}
	return s, why, true
}

// Display is the score on a 0–100 scale for people: 50 is a car with
// nothing for or against it. It bends towards the ends instead of being cut
// off, so better cars always show higher: 50 + 50·tanh(S/displaySpread),
// 5 a point near the middle. Only a car too suspicious to rate is 0.
func Display(score float64) int {
	if math.IsInf(score, -1) || math.IsNaN(score) {
		return 0
	}
	return int(math.Round(50 + shown(score)))
}

const displaySpread = 10.0

// shown is how far from 50 Display puts score.
func shown(score float64) float64 { return 50 * math.Tanh(score/displaySpread) }

// DisplayPoints is what a component's points are worth on Display's scale
// in a car whose total is score: its share of how far the total is from 50,
// so the parts add up to the total shown.
func DisplayPoints(points, score float64) float64 {
	if score == 0 || math.IsInf(score, 0) || math.IsNaN(score) {
		return 50 / displaySpread * points
	}
	return points * shown(score) / score
}

// Candidate reports whether a car rated from its list page alone looks
// worth reading its ad page for: a price known and no worse than Fair
// (nor too low to trust), no risky engine, and a mileage not far beyond its
// age. The ad page then adds the gearbox and equipment.
func Candidate(r Ratings) bool {
	switch {
	case r.Price == nil || r.Price.Verdict == Suspicious || r.Price.Verdict >= Pricey:
		return false
	case r.Engine != nil && r.Engine.Rating == RatingRisky:
		return false
	case r.Mileage != nil && r.Mileage.X < -0.5:
		return false
	}
	return true
}
