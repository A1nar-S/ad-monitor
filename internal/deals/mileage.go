package deals

import (
	"fmt"
	"math"
	"time"
)

// Now is the clock mileage is judged by (a car's age); tests pin it.
var Now = time.Now

const (
	// kmPerYearUsual is what a car usually covers in a year.
	kmPerYearUsual = 18000.0
	// highKm: past this the engine and running gear are worn whatever the
	// age, which costs a little more.
	highKm = 300000.0
	// oldCar: below-usual mileage on a car older than this earns at most
	// half the bonus, as a rolled-back odometer is likelier than a garage
	// queen.
	oldCar = 12.0
)

// MileageRating is how a car's mileage compares with what is usual for its
// age. X is its part of the score, from -1 (far too much for its age) to +1.
type MileageRating struct {
	Km       float64
	Age      float64 // years, at least 1
	Expected float64 // usual km at that age
	Ratio    float64 // Km / Expected
	X        float64
}

// RateMileage judges c's mileage against its age:
//
//	age      = max(1, years since its model year)
//	expected = 18 000 km × age
//	base     = clamp(−log₂(km / expected), −1, 1)     // half → +1, double → −1
//	weight   = clamp(10 / (age + 5), 0.5, 1.5)         // 2 y → 1.43, 5 y → 1, 20 y → 0.5
//	x        = base × weight                           // when over the usual
//	x        = base × min(weight, 1)                   // when under; at most 0.5 past 12 y
//	x       −= 0.25 past 300 000 km
//
// So 150 000 km on a 3-year-old car costs the most there is, while 400 000
// km on a 20-year-old one costs only a little: wear in a few years says
// hard use, over twenty it is ordinary. ok is false without a mileage.
func RateMileage(c Car) (MileageRating, bool) {
	if c.MileageKm <= 0 || c.Year <= 0 {
		return MileageRating{}, false
	}
	now := Now()
	age := float64(now.Year()-c.Year) + float64(now.YearDay())/365
	age = max(1, age)
	m := MileageRating{Km: c.MileageKm, Age: age, Expected: kmPerYearUsual * age}
	m.Ratio = m.Km / m.Expected
	base := clamp(-math.Log2(m.Ratio), -1, 1)
	weight := clamp(10/(age+5), 0.5, 1.5)
	if base < 0 {
		m.X = base * weight
	} else {
		m.X = base * min(weight, 1)
		if age > oldCar {
			m.X = min(m.X, 0.5)
		}
	}
	if m.Km > highKm {
		m.X -= 0.25
	}
	m.X = clamp(m.X, -1.25, 1)
	return m, true
}

func clamp(x, lo, hi float64) float64 { return max(lo, min(hi, x)) }

// Mileage levels, by X.
func (m MileageRating) label() string {
	switch {
	case m.X >= 0.4:
		return "👍 Low mileage"
	case m.X > -0.3:
		return "😐 Usual mileage"
	case m.X > -0.7:
		return "👎 High mileage"
	}
	return "💩 Very high mileage"
}

// Text is the mileage line of an alert, e.g. "👎 High mileage: 210k km at
// 6 years, 1.9× the usual 108k km for its age."
func (m MileageRating) Text() string {
	return fmt.Sprintf("%s: %sk km at %d years, %.1f× the usual %sk km for its age.",
		m.label(), thousands(m.Km), int(math.Round(m.Age)), m.Ratio, thousands(m.Expected))
}
