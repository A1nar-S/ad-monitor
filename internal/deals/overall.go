package deals

import (
	"fmt"
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

func (b Buy) String() string {
	return [...]string{"❌ Likely a bad buy", "😐 Average buy", "✅ Potentially a good buy", "🚩 Too cheap to trust"}[b]
}

// Ratings are what is known about one car; nil = unknown.
type Ratings struct {
	Price     *PriceRating
	Engine    *Note
	Gearbox   *Note
	Equipment *EquipmentRating
}

// Rate gathers everything there is to say about c.
func Rate(c Car, peers []Peer) Ratings {
	var r Ratings
	if p, ok := RatePrice(c, peers); ok {
		r.Price = &p
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

// Overall weighs the ratings into one verdict: price counts most (up to ±2),
// a risky engine as much as a high price, the gearbox and the equipment
// one step each. It needs two of the four known, or there is too little to
// go on, and a suspiciously low price overrides everything.
func (r Ratings) Overall() (Buy, []string, bool) {
	score, why, ok := r.judge()
	switch {
	case !ok:
		return 0, nil, false
	case r.Price != nil && r.Price.Verdict == Suspicious:
		return CheckFirst, why, true
	case score >= 2:
		return GoodBuy, why, true
	case score <= -2:
		return BadBuy, why, true
	}
	return AverageBuy, why, true
}

// Score ranks cars by how good a buy they look, highest first: Overall's
// points, broken by how far below similar ads the price is. Suspiciously
// cheap ones and those with too little known rank last.
func (r Ratings) Score() float64 {
	score, _, ok := r.judge()
	if !ok || r.Price != nil && r.Price.Verdict == Suspicious {
		return math.Inf(-1)
	}
	s := float64(score)
	if r.Price != nil {
		s -= r.Price.Diff // 14% below adds 0.14
	}
	return s
}

func (r Ratings) judge() (int, []string, bool) {
	known := 0
	for _, k := range []bool{r.Price != nil, r.Engine != nil, r.Gearbox != nil, r.Equipment != nil} {
		if k {
			known++
		}
	}
	if known < 2 {
		return 0, nil, false
	}
	var score int
	var why []string
	if p := r.Price; p != nil {
		pct := int(math.Round(math.Abs(p.Diff) * 100))
		switch p.Verdict {
		case Suspicious:
			return 0, []string{fmt.Sprintf("%d%% below similar ads, which usually means damage, a scam or a typo", pct)}, true
		case Steal:
			score += 2
			why = append(why, fmt.Sprintf("%d%% below similar ads", pct))
		case Good:
			score++
			why = append(why, fmt.Sprintf("%d%% below similar ads", pct))
		case Fair:
			why = append(why, "priced like similar ads")
		case Pricey:
			score--
			why = append(why, fmt.Sprintf("%d%% above similar ads", pct))
		case Overpriced:
			score -= 2
			why = append(why, fmt.Sprintf("%d%% above similar ads", pct))
		}
	}
	part := func(n *Note, what string, risky int) {
		if n == nil {
			return
		}
		switch n.Rating {
		case RatingGood:
			score++
			why = append(why, fmt.Sprintf("durable %s (%s)", what, n.Name))
		case RatingRisky:
			score -= risky
			why = append(why, fmt.Sprintf("risky %s (%s)", what, n.Name))
		case RatingMixed:
			why = append(why, fmt.Sprintf("%s depends on the version", what))
		}
	}
	part(r.Engine, "engine", 2)
	part(r.Gearbox, "gearbox", 1)
	if e := r.Equipment; e != nil {
		switch e.Level {
		case WellEquipped:
			score++
			why = append(why, "well equipped")
		case Basic:
			score--
			why = append(why, "basic equipment")
		}
	}
	return score, why, true
}

// Text is the overall line, as in an alert.
func (r Ratings) Text() (string, bool) {
	b, why, ok := r.Overall()
	if !ok {
		return "", false
	}
	return overallText(b, why), true
}

// overallText is the overall line, e.g. "✅ Potentially a good buy: 14% below
// similar ads, durable engine (2.0d B47). Still have it inspected."
func overallText(b Buy, why []string) string {
	s := b.String()
	if len(why) > 0 {
		s += ": " + strings.Join(why, ", ")
	}
	s += "."
	switch b {
	case GoodBuy:
		s += " Still have it inspected before paying."
	case CheckFirst:
		s += " Inspect it before anything else."
	}
	return s
}
