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
func (r InspectionRating) Text() string { return r.TextIn(En) }

// TextIn is the inspection line in l.
func (r InspectionRating) TextIn(l Lang) string {
	p := l.ph()
	switch {
	case !r.Valid && !r.Until.IsZero():
		return fmt.Sprintf(p.inspectionGone, r.Until.Format("01.2006"))
	case !r.Valid:
		return p.inspectionNone
	case r.Until.IsZero():
		return p.inspectionValid
	case r.MonthsLeft < 3:
		return fmt.Sprintf(p.inspectionSoon, r.Until.Format("01.2006"), p.monthsLeft(r.MonthsLeft))
	}
	return fmt.Sprintf(p.inspectionUntil, r.Until.Format("01.2006"), p.monthsLeft(r.MonthsLeft))
}
