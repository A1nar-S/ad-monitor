package deals

import (
	"fmt"
	"time"
)

// InspectionRating is what a car's technical inspection says. X is its part
// of the score, which only ever costs: a valid one is what every car should
// have, so 0; −0.25 when it runs out within 3 months; −1 without one (it
// may not be driven until it passes, and passing may need repairs).
type InspectionRating struct {
	Valid      bool
	Until      time.Time // zero = not said
	MonthsLeft int       // with Until
	X          float64
}

// RateInspection judges c's inspection; ok is false when unknown (ss.com:
// its ad page was not read). An end already past counts as none.
func RateInspection(c Car) (InspectionRating, bool) {
	switch c.Inspection {
	case NotInspected:
		return InspectionRating{X: -1}, true
	case Inspected:
	default:
		return InspectionRating{}, false
	}
	r := InspectionRating{Valid: true, Until: c.InspectionUntil}
	if c.InspectionUntil.IsZero() {
		return r, true
	}
	now := Now()
	// It runs to the end of its month.
	r.MonthsLeft = (c.InspectionUntil.Year()-now.Year())*12 + int(c.InspectionUntil.Month()-now.Month())
	switch {
	case r.MonthsLeft < 0:
		return InspectionRating{Until: c.InspectionUntil, X: -1}, true
	case r.MonthsLeft < 3:
		r.X = -0.25
	}
	return r, true
}

// Text is the inspection line of an alert, e.g. "👍 Valid until 02.2027 (15
// months left)."
func (r InspectionRating) Text() string {
	switch {
	case !r.Valid && !r.Until.IsZero():
		return fmt.Sprintf("👎 Ran out in %s: it may not be driven until it passes again, which may need repairs.", r.Until.Format("01.2006"))
	case !r.Valid:
		return "👎 No valid technical inspection: it may not be driven until it passes, which may need repairs."
	case r.Until.IsZero():
		return "👍 Valid technical inspection."
	case r.MonthsLeft < 3:
		return fmt.Sprintf("😐 Valid until %s, but runs out soon (%d months left): budget for the next one.", r.Until.Format("01.2006"), r.MonthsLeft)
	}
	return fmt.Sprintf("👍 Valid until %s (%d months left).", r.Until.Format("01.2006"), r.MonthsLeft)
}
