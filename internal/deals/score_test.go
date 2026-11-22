package deals

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain pins the clock mileage is judged by, and the inspection table
// to a made-up one, so tests don't change with each year's data.
func TestMain(m *testing.M) {
	Now = func() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) }
	loadFailTable = func() (*failTable, error) { return newFailTable([]byte(testFailJSON)) }
	os.Exit(m.Run())
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestRateMileage(t *testing.T) {
	tests := []struct {
		name  string
		year  int
		km    float64
		wantX float64
	}{
		{"3 years, 150k: hard use, the most it costs", 2023, 150000, -1.125},
		{"20 years, 400k: ordinary for its age, a little for the wear", 2006, 400000, -0.295},
		{"10 years, 200k: usual", 2016, 200000, -0.018},
		{"under 5 years, half the usual: the full bonus", 2022, 44000, 1},
		{"20 years, 150k: low, but capped (odometer?)", 2006, 150000, 0.5},
		{"this year's car: at least one year old", 2026, 18000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := RateMileage(Car{Year: tt.year, MileageKm: tt.km})
			if !ok || !near(m.X, tt.wantX) {
				t.Errorf("X = %.3f (ratio %.2f, age %.1f), want %.3f", m.X, m.Ratio, m.Age, tt.wantX)
			}
		})
	}
	if _, ok := RateMileage(Car{Year: 2015}); ok {
		t.Error("rated an unknown mileage")
	}
}

// The same mileage costs a new car more than an old one.
func TestMileageWeighsAge(t *testing.T) {
	for _, km := range []float64{150000, 250000, 350000} {
		prev := math.Inf(-1)
		for _, year := range []int{2023, 2019, 2014, 2008, 2002} {
			m, _ := RateMileage(Car{Year: year, MileageKm: km})
			if m.X < prev-1e-9 {
				t.Errorf("%.0f km: %d scores %.3f, below the newer car's %.3f", km, year, m.X, prev)
			}
			prev = m.X
		}
	}
}

func TestComponents(t *testing.T) {
	good := &Note{Name: "B47", Rating: RatingGood}
	risky := &Note{Name: "N47", Rating: RatingRisky}
	r := Ratings{
		Price:     &PriceRating{Verdict: Good, Diff: -0.1, Z: 0.5},
		Mileage:   &MileageRating{Km: 300000, X: -0.5},
		Engine:    risky,
		Gearbox:   good,
		Equipment: &EquipmentRating{Level: Average, Better: 6, Of: 8},
	}
	want := map[string]float64{
		"price":     3 * 0.5,   // half a tolerance below
		"mileage":   2 * -0.5,  //
		"engine":    2 * -1.5,  // risky, worn: 300k km is past 1.5 × 200k
		"gearbox":   1.5 * 1,   //
		"equipment": 1.5 * 0.5, // beats 6 of 8: 2 × 0.75 − 1
	}
	var sum float64
	for _, c := range r.Components() {
		if !near(c.Points, want[c.Key]) {
			t.Errorf("%s = %.3f, want %.3f", c.Key, c.Points, want[c.Key])
		}
		sum += c.Points
	}
	if !near(r.Score(), sum) {
		t.Errorf("score %.3f is not the sum %.3f", r.Score(), sum)
	}
}

func TestDisplay(t *testing.T) {
	for score, want := range map[float64]int{0: 50, 1: 55, 4: 69, -2: 40, 11.5: 91, -14.5: 5, 20: 98, -20: 2, math.Inf(-1): 0} {
		if got := Display(score); got != want {
			t.Errorf("Display(%v) = %d, want %d", score, got, want)
		}
	}
	if Display(30) <= Display(20) {
		t.Error("a better car must show higher, not be cut off at 100")
	}
}

// The parts on Display's scale add up to how far the total is from 50.
func TestDisplayPointsAddUp(t *testing.T) {
	for _, parts := range [][]float64{{3, 2, 2, 1.5, 1.5, 1.5}, {3, -2, 1.5}, {-3, -2, -2, -1.5}, {1, -1}} {
		var score float64
		for _, p := range parts {
			score += p
		}
		var sum float64
		for _, p := range parts {
			sum += DisplayPoints(p, score)
		}
		if want := 50 * math.Tanh(score/displaySpread); !near(sum, want) {
			t.Errorf("%v: parts add up to %.3f, want %.3f", parts, sum, want)
		}
	}
	if got := DisplayPoints(3, 0); got != 15 {
		t.Errorf("price +1 in a car scoring 0 = %v, want 15", got)
	}
}

func priced(median, price float64) *PriceRating { r := priceVs(median, price); return &r }

func TestCandidate(t *testing.T) {
	fair := &PriceRating{Verdict: Fair, Diff: 0.03}
	tests := []struct {
		name string
		r    Ratings
		want bool
	}{
		{"fair price, nothing else known", Ratings{Price: fair}, true},
		{"no price", Ratings{Engine: &Note{Rating: RatingGood}}, false},
		{"pricey", Ratings{Price: &PriceRating{Verdict: Pricey, Diff: 0.1}}, false},
		{"too cheap to trust", Ratings{Price: &PriceRating{Verdict: Suspicious, Diff: -0.5}}, false},
		{"300 € over a 1 500 € car", Ratings{Price: priced(1500, 1800)}, true},
		{"2 500 € over a 22 500 € car", Ratings{Price: priced(22500, 25000)}, false},
		{"risky engine", Ratings{Price: fair, Engine: &Note{Rating: RatingRisky}}, false},
		{"far too many km", Ratings{Price: fair, Mileage: &MileageRating{X: -0.8}}, false},
		{"a few too many km", Ratings{Price: fair, Mileage: &MileageRating{X: -0.3}}, true},
	}
	for _, tt := range tests {
		if got := Candidate(tt.r); got != tt.want {
			t.Errorf("%s: Candidate = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRateInspection(t *testing.T) {
	month := func(s string) time.Time { m, _ := time.Parse("2006-01", s); return m }
	tests := []struct {
		name  string
		car   Car
		wantX float64
		text  string
	}{
		{"none", Car{Inspection: NotInspected}, -1, "No valid technical inspection"},
		{"over a year left", Car{Inspection: Inspected, InspectionUntil: month("2028-02")}, 0, "Valid until 02.2028 (15 months left)."},
		{"4 months left", Car{Inspection: Inspected, InspectionUntil: month("2027-03")}, 0, "Valid until 03.2027 (4 months left)."},
		{"runs out this winter", Car{Inspection: Inspected, InspectionUntil: month("2027-01")}, -0.25, "Valid until 01.2027, but runs out soon"},
		{"already ran out", Car{Inspection: Inspected, InspectionUntil: month("2026-09")}, -1, "Ran out in 09.2026"},
		{"valid, no end said", Car{Inspection: Inspected}, 0, "Valid technical inspection."},
	}
	for _, tt := range tests {
		r, ok := RateInspection(tt.car)
		if !ok || !near(r.X, tt.wantX) || !strings.HasPrefix(r.Text(), tt.text) {
			t.Errorf("%s: X %.2f %q, want %.2f %q", tt.name, r.X, r.Text(), tt.wantX, tt.text)
		}
	}
	if _, ok := RateInspection(Car{}); ok {
		t.Error("rated an unknown inspection")
	}
	// No inspection weighs like a steep price: a fair, durable car without
	// one is no longer average.
	good := &Note{Name: "B47", Rating: RatingGood}
	r := Ratings{Price: &PriceRating{Verdict: Fair}, Engine: good, Inspection: &InspectionRating{X: -1}}
	if b, why, _ := r.Overall(); b != AverageBuy || !strings.Contains(strings.Join(why, ", "), "no valid technical inspection") {
		t.Errorf("without inspection: %v %v", b, why)
	}
}
