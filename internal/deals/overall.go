package deals

import (
	"math"
	"strings"
)

// Buy is the overall verdict on a car ad.
type Buy int

const (
	BadBuy Buy = iota
	AverageBuy
	GoodBuy
	CheckFirst // the price is too low to trust: inspect before anything else
)

func (b Buy) String() string { return enPhrases.buy[b] }

// Ratings are what is known about one car; nil = unknown.
type Ratings struct {
	Price      *PriceRating
	Mileage    *MileageRating
	Inspection *InspectionRating
	Engine     *Note
	Gearbox    *Note
	Equipment  *EquipmentRating
}

// Rate gathers everything there is to say about c.
func Rate(c Car, peers []Peer) Ratings {
	var r Ratings
	if p, ok := RatePrice(c, peers); ok {
		r.Price = &p
	}
	if m, ok := RateMileage(c); ok {
		r.Mileage = &m
	}
	if i, ok := RateInspection(c); ok {
		r.Inspection = &i
	}
	if n, ok := RateEngine(c); ok {
		r.Engine = &n
	}
	if n, ok := RateGearbox(c); ok {
		r.Gearbox = &n
	}
	if e, ok := RateEquipment(c, peers); ok {
		r.Equipment = &e
	}
	return r
}

// Overall turns the score (see score.go) into one verdict. It needs two of
// its parts known, or there is too little to go on, and a
// suspiciously low price overrides everything.
func (r Ratings) Overall() (Buy, []string, bool) { return r.OverallIn(En) }

// OverallIn is Overall with the reasons in l.
func (r Ratings) OverallIn(l Lang) (Buy, []string, bool) {
	score, why, ok := r.judge(l)
	switch {
	case !ok:
		return 0, nil, false
	case r.Price != nil && r.Price.Verdict == Suspicious:
		return CheckFirst, why, true
	case score >= goodScore:
		return GoodBuy, why, true
	case score <= badScore:
		return BadBuy, why, true
	}
	return AverageBuy, why, true
}

// Score ranks cars by how good a buy they look, highest first: the
// weighted sum of score.go. Suspiciously cheap ones and those with too
// little known rank last.
func (r Ratings) Score() float64 {
	score, _, ok := r.judge(En)
	if !ok || r.Price != nil && r.Price.Verdict == Suspicious {
		return math.Inf(-1)
	}
	return score
}

// Text is the overall line, as in an alert.
func (r Ratings) Text() (string, bool) { return r.TextIn(En) }

// TextIn is the overall line in l.
func (r Ratings) TextIn(l Lang) (string, bool) {
	b, why, ok := r.OverallIn(l)
	if !ok {
		return "", false
	}
	return overallText(b, why, l), true
}

// overallText is the overall line, e.g. "Potentially a good buy: 14% below
// similar ads, durable engine (2.0d B47). Still have it inspected."
func overallText(b Buy, why []string, l Lang) string {
	p := l.ph()
	s := p.buy[b]
	if len(why) > 0 {
		s += ": " + strings.Join(why, ", ")
	}
	s += "."
	switch b {
	case GoodBuy:
		s += p.overallGood
	case CheckFirst:
		s += p.overallCheck
	}
	return s
}
