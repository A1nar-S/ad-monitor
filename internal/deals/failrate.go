package deals

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
)

// How often a model fails its technical inspection in Latvia at the first
// attempt (the main check, not the re-check after repairs), against
// all cars of its model year and fuel: CSDD's open data ("Tehniskās
// apskates", data.gov.lv, CC0), counted by `admin csdd-inspections`. It is
// about the model, not this car: the data has no VIN or mileage. It also
// reflects how such cars are kept, which a buyer pays for all the same.
//
//go:embed codebook/inspections.json
var inspectionsJSON []byte

// InspectionStats is codebook/inspections.json.
type InspectionStats struct {
	Source string          `json:"source"`
	Years  [2]int          `json:"years"`  // the inspection years counted, inclusive
	Base   map[Fuel]Series `json:"base"`   // every car counted, by fuel
	Models []ModelStats    `json:"models"` // one per make and model
}

// ModelStats are one model's main inspections, by fuel.
type ModelStats struct {
	Make  string          `json:"make"`
	Model string          `json:"model"` // matched as a prefix: "Golf" covers "Golf 5"
	Fuels map[Fuel]Series `json:"fuels"`
}

// Series counts main inspections by model year: N[i] of cars from model
// year From+i, Fails[i] of them failed (a re-inspection was ordered).
type Series struct {
	From  int   `json:"from"`
	N     []int `json:"n"`
	Fails []int `json:"fails"`
}

// Add counts one inspection of a car from model year year.
func (s *Series) Add(year int, failed bool) {
	switch {
	case len(s.N) == 0:
		s.From = year
	case year < s.From:
		pad := s.From - year
		s.N = append(make([]int, pad), s.N...)
		s.Fails = append(make([]int, pad), s.Fails...)
		s.From = year
	}
	for year-s.From >= len(s.N) {
		s.N, s.Fails = append(s.N, 0), append(s.Fails, 0)
	}
	s.N[year-s.From]++
	if failed {
		s.Fails[year-s.From]++
	}
}

// Total is how many inspections s counts.
func (s Series) Total() int {
	t := 0
	for _, n := range s.N {
		t += n
	}
	return t
}

// at is year's inspections and fails.
func (s Series) at(year int) (n, fails int) {
	if i := year - s.From; i >= 0 && i < len(s.N) {
		return s.N[i], s.Fails[i]
	}
	return 0, 0
}

func (s Series) validate() error {
	if len(s.N) != len(s.Fails) {
		return fmt.Errorf("%d counts but %d fails", len(s.N), len(s.Fails))
	}
	for i := range s.N {
		if s.Fails[i] < 0 || s.Fails[i] > s.N[i] {
			return fmt.Errorf("year %d: %d fails of %d", s.From+i, s.Fails[i], s.N[i])
		}
	}
	return nil
}

func (st *InspectionStats) validate() error {
	if st.Years[0] == 0 || st.Years[0] > st.Years[1] {
		return fmt.Errorf("bad years %v", st.Years)
	}
	for f, s := range st.Base {
		if !Knows("fuel", string(f)) {
			return fmt.Errorf("base: unknown fuel %q", f)
		}
		if err := s.validate(); err != nil {
			return fmt.Errorf("base %s: %w", f, err)
		}
	}
	for _, m := range st.Models {
		if m.Make == "" || compact(m.Model) == "" {
			return fmt.Errorf("model %q %q: make and model are required", m.Make, m.Model)
		}
		for f, s := range m.Fuels {
			if _, ok := st.Base[f]; !ok {
				return fmt.Errorf("%s %s: fuel %q has no base", m.Make, m.Model, f)
			}
			if err := s.validate(); err != nil {
				return fmt.Errorf("%s %s %s: %w", m.Make, m.Model, f, err)
			}
		}
	}
	return nil
}

// failTable is the loaded stats with each make's models ready to match.
type failTable struct {
	*InspectionStats
	byMake map[string][]keyed // by compact make
}

type keyed struct {
	key string // compact model, without the make
	*ModelStats
}

func newFailTable(raw []byte) (*failTable, error) {
	var st InspectionStats
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("inspection stats: %w", err)
	}
	if err := st.validate(); err != nil {
		return nil, fmt.Errorf("inspection stats: %w", err)
	}
	t := &failTable{InspectionStats: &st, byMake: map[string][]keyed{}}
	for i := range st.Models {
		m := &st.Models[i]
		mk := compact(m.Make)
		t.byMake[mk] = append(t.byMake[mk], keyed{ModelKey(m.Make, m.Model), m})
	}
	return t, nil
}

var loadFailTable = sync.OnceValues(func() (*failTable, error) { return newFailTable(inspectionsJSON) })

// compact is s in lower-case letters and digits only: "RAV 4", "Rav-4" and
// "RAV4" are all "rav4". Between two numbers a "." stays, so "80 1,8 S" is
// "80.1.8s", not "8018s".
func compact(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && unicode.IsDigit(r) && b.Len() > 0 && isDigit(b.String()[b.Len()-1]) {
			b.WriteByte('.')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// ModelKey is how a model is matched: compact, without a leading make
// ("Mazda6" is "6", like CSDD's "MAZDA 6").
func ModelKey(brand, model string) string {
	k, mk := compact(model), compact(brand)
	if rest, ok := strings.CutPrefix(k, mk); ok && rest != "" {
		return rest
	}
	return k
}

// CoversModel reports whether key (a ModelKey) covers model's key: equal,
// or a prefix that does not cut a number ("golf" covers "golf5", "6" does
// not cover "626").
func CoversModel(key, model string) bool {
	rest, ok := strings.CutPrefix(model, key)
	if !ok || key == "" {
		return false
	}
	return rest == "" || !isDigit(key[len(key)-1]) || !isDigit(rest[0])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// lookup is the models that cover c's, the most specific (longest key)
// first: a "Passat Variant" too rare for its years falls back to "Passat".
func (t *failTable) lookup(c Car) []*ModelStats {
	model := ModelKey(c.Make, c.Model)
	var hits []keyed
	for _, k := range t.byMake[compact(c.Make)] {
		if CoversModel(k.key, model) {
			hits = append(hits, k)
		}
	}
	slices.SortStableFunc(hits, func(a, b keyed) int { return len(b.key) - len(a.key) })
	out := make([]*ModelStats, len(hits))
	for i, h := range hits {
		out[i] = h.ModelStats
	}
	return out
}

// A model is rated on at least minInspections main inspections of cars
// within ±1 model year (±2 when too few), of c's fuel or, when still too
// few, of any. Its odds of failing are compared with what all cars of those
// years and fuels would have: half the odds is +1, double −1, the same for
// old and young cars. failPrior fails and passes added to both sides keep a
// model with few fails expected near "usual" (one car is counted in each
// year it was inspected, so the counts overstate the evidence). "All cars"
// are those of models the ads know (trucks and the like are not counted).
//
//	x = clamp(−log₂(((fails+k) / (passes+k)) / ((expected+k) / (n−expected+k))), −1, 1),  k = failPrior
//
// On the 2022–2024 table that makes 29% of ratings "often fails", 48% "usual".
const (
	minInspections = 400 // fewer, and a young model's few fails are noise
	failPrior      = 10.0
)

// ReliabilityRating is how a car's model does at inspection, first attempt.
type ReliabilityRating struct {
	FailRate    float64 // of these cars, 0..1
	Typical     float64 // of all cars of the same years and fuels
	Inspections int
	YearMin     int // the model years that had any
	YearMax     int
	X           float64
	From, To    int // the inspection years
}

// RateReliability looks up how often c's model fails its technical
// inspection at the first attempt; ok is false when it is not in the data or too rare.
func RateReliability(c Car) (ReliabilityRating, bool) {
	t, err := loadFailTable()
	if err != nil || c.Year == 0 {
		return ReliabilityRating{}, false // a broken table fails its test; rating goes on without it
	}
	for _, m := range t.lookup(c) {
		if r, ok := rateModel(t, m, c); ok {
			return r, true
		}
	}
	return ReliabilityRating{}, false
}

// rateModel rates c on m's inspections; ok is false when too few.
func rateModel(t *failTable, m *ModelStats, c Car) (ReliabilityRating, bool) {
	var tries [][]Fuel
	if _, ok := m.Fuels[c.Fuel]; ok {
		tries = append(tries, []Fuel{c.Fuel})
	}
	tries = append(tries, slices.Sorted(maps.Keys(m.Fuels))) // sorted: the sums come out the same every time
	for _, fuels := range tries {
		for _, span := range []int{1, 2} {
			r := ReliabilityRating{From: t.Years[0], To: t.Years[1]}
			var fails int
			var expected float64
			for _, f := range fuels {
				base := t.Base[f]
				for y := c.Year - span; y <= c.Year+span; y++ {
					n, fl := m.Fuels[f].at(y)
					bn, bf := base.at(y)
					if n == 0 || bn == 0 {
						continue
					}
					if r.Inspections == 0 || y < r.YearMin {
						r.YearMin = y
					}
					r.YearMax = max(r.YearMax, y)
					r.Inspections += n
					fails += fl
					expected += float64(n) * float64(bf) / float64(bn)
				}
			}
			if r.Inspections < minInspections {
				continue
			}
			n := float64(r.Inspections)
			r.FailRate = float64(fails) / n
			r.Typical = expected / n
			odds := (float64(fails) + failPrior) / (n - float64(fails) + failPrior)
			typicalOdds := (expected + failPrior) / (n - expected + failPrior)
			r.X = clamp(-math.Log2(odds/typicalOdds), -1, 1)
			return r, true
		}
	}
	return ReliabilityRating{}, false
}

// level is the line's label: 0 often fails, 1 usual, 2 rarely fails.
func (r ReliabilityRating) level() int {
	switch {
	case r.X <= -0.4:
		return 0
	case r.X >= 0.4:
		return 2
	}
	return 1
}

// Text is the alert line, e.g. "Often fails inspection: 52% of such cars
// (2009–2011) fail the technical inspection at the first attempt, 41% of all cars that
// age (CSDD 2022–2024, 3 120 inspections)."
func (r ReliabilityRating) Text() string { return r.TextIn(En) }

// TextIn is the alert line in l.
func (r ReliabilityRating) TextIn(l Lang) string {
	p := l.ph()
	years := strconv.Itoa(r.YearMin)
	if r.YearMax != r.YearMin {
		years += "–" + strconv.Itoa(r.YearMax)
	}
	source := fmt.Sprintf("%d–%d", r.From, r.To)
	return fmt.Sprintf(p.reliabilityLine, p.reliability[r.level()], l.percent(r.FailRate), years,
		l.percent(r.Typical), source, textutil.Grouped(r.Inspections), p.inspections(r.Inspections))
}

// percent writes a 0..1 share as a percentage, with one decimal below 10
// so young cars' small rates stay apart: "0.7", "0,7" in Latvian, "22".
func (l Lang) percent(x float64) string {
	if p := math.Round(x * 1000); p > 0 && p < 100 {
		return l.decimal(p / 10)
	}
	return strconv.Itoa(int(math.Round(x * 100)))
}
