package deals

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
)

// The reliability codebook: what engines and gearboxes are known for, as a
// mechanic or an owners' forum would put it. It is reputation, not an
// inspection of the car, and the list page gives only a make, a year, a
// size and a fuel, not the engine code, so an entry names what such a car
// most likely has. It is in this package's English vocabulary: sources
// translate their own words into it. Edit the JSON to correct or add
// entries.
//
//go:embed codebook/reliability.json
var codebookJSON []byte

// Rating is an engine's or gearbox's reputation.
type Rating string

const (
	RatingGood  Rating = "good"  // known as durable
	RatingOK    Rating = "ok"    // fine, with known wear items to check
	RatingMixed Rating = "mixed" // depends on which version it is; see the notes
	RatingRisky Rating = "risky" // known for costly failures
)

// Spec is one codebook entry: the cars it covers and what they are known for.
type Spec struct {
	Makes   []string  `json:"makes"`             // "*" = any make
	Models  []string  `json:"models,omitempty"`  // prefixes of the model name ("Passat" covers "Passat (B6)"); none = any
	Fuel    Fuel      `json:"fuel,omitempty"`    // petrol, diesel, hybrid or electric; "" = any
	Litres  []float64 `json:"litres,omitempty"`  // engine sizes; none = any
	Years   [2]int    `json:"years"`             // first and last model year, inclusive
	Gearbox Gearbox   `json:"gearbox,omitempty"` // gearbox entries only: automatic or manual
	Name    string    `json:"name"`              // what it most likely is, e.g. "2.0d N47"
	Rating  Rating    `json:"rating"`
	Notes   string    `json:"notes"`
	NameLv  string    `json:"name_lv,omitempty"` // Name in Latvian, when it has words to translate
	NotesLv string    `json:"notes_lv"`          // Notes in Latvian
}

type codebook struct {
	Engines   []Spec `json:"engines"`
	Gearboxes []Spec `json:"gearboxes"`
}

var loadCodebook = sync.OnceValues(func() (*codebook, error) {
	var cb codebook
	if err := json.Unmarshal(codebookJSON, &cb); err != nil {
		return nil, fmt.Errorf("reliability codebook: %w", err)
	}
	for _, list := range [][]Spec{cb.Engines, cb.Gearboxes} {
		for _, s := range list {
			if err := s.validate(); err != nil {
				return nil, fmt.Errorf("reliability codebook: %q: %w", s.Name, err)
			}
		}
	}
	return &cb, nil
})

func (s Spec) validate() error {
	switch {
	case len(s.Makes) == 0:
		return fmt.Errorf("no makes")
	case s.Years[0] == 0 || s.Years[0] > s.Years[1]:
		return fmt.Errorf("bad years %v", s.Years)
	case enPhrases.rating[s.Rating] == "":
		return fmt.Errorf("unknown rating %q", s.Rating)
	case s.Fuel != "" && !Knows("fuel", string(s.Fuel)):
		return fmt.Errorf("fuel %q is not one of petrol, diesel, hybrid, electric", s.Fuel)
	case s.Gearbox != "" && !Knows("gearbox", string(s.Gearbox)):
		return fmt.Errorf("gearbox %q is not automatic or manual", s.Gearbox)
	case s.Name == "" || s.Notes == "" || s.NotesLv == "":
		return fmt.Errorf("name, notes and notes_lv are required")
	}
	return nil
}

// Note is what the codebook says about one car's engine or gearbox.
type Note struct {
	Name   string
	Rating Rating
	Notes  string
	// In Latvian; NameLv "" = the same as Name.
	NameLv, NotesLv string
}

// Text is the alert line, e.g. "👎 Risky: 2.0d N47. Timing chain ...".
func (n Note) Text() string { return n.TextIn(En) }

// TextIn is the alert line in l.
func (n Note) TextIn(l Lang) string {
	name, notes := n.Name, n.Notes
	if l == Lv {
		name, notes = cmp.Or(n.NameLv, n.Name), n.NotesLv
	}
	return fmt.Sprintf("%s: %s. %s", l.ph().rating[n.Rating], name, notes)
}

// NameIn is the engine's or gearbox's name in l.
func (n Note) NameIn(l Lang) string {
	if l == Lv {
		return cmp.Or(n.NameLv, n.Name)
	}
	return n.Name
}

// RateEngine looks up the reputation of c's engine.
func RateEngine(c Car) (Note, bool) {
	cb, err := loadCodebook()
	if err != nil {
		return Note{}, false // a broken codebook fails its test; alerts still go out
	}
	return lookup(cb.Engines, c)
}

// RateGearbox looks up the reputation of c's gearbox. Only automatics with
// a known reputation are listed, and the gearbox is known only once the
// ad page was read.
func RateGearbox(c Car) (Note, bool) {
	cb, err := loadCodebook()
	if err != nil || c.Gearbox == "" {
		return Note{}, false
	}
	return lookup(cb.Gearboxes, c)
}

// lookup returns the most specific matching entry: one naming models beats
// one for a whole make, which beats one for any make; ties go to the first.
func lookup(specs []Spec, c Car) (Note, bool) {
	best, bestScore := -1, -1
	for i, s := range specs {
		if !s.matches(c) {
			continue
		}
		if score := specificity(s); score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return Note{}, false
	}
	s := specs[best]
	return Note{Name: s.Name, Rating: s.Rating, Notes: s.Notes, NameLv: s.NameLv, NotesLv: s.NotesLv}, true
}

// specificity ranks entries that match the same car: models > make > any.
func specificity(s Spec) int {
	score := 0
	if len(s.Models) > 0 {
		score += 2
	}
	if !slices.Contains(s.Makes, "*") {
		score++
	}
	return score
}

func (s Spec) matches(c Car) bool {
	if !slices.ContainsFunc(s.Makes, func(m string) bool { return m == "*" || strings.EqualFold(m, c.Make) }) {
		return false
	}
	if c.Year < s.Years[0] || c.Year > s.Years[1] {
		return false
	}
	if s.Fuel != "" && s.Fuel != c.Fuel {
		return false
	}
	if s.Gearbox != "" && s.Gearbox != c.Gearbox {
		return false
	}
	if len(s.Litres) > 0 && !slices.ContainsFunc(s.Litres, func(l float64) bool { return math.Abs(l-c.EngineL) < 0.051 }) {
		return false
	}
	model := strings.ToLower(c.Model)
	return len(s.Models) == 0 || slices.ContainsFunc(s.Models, func(m string) bool { return strings.HasPrefix(model, strings.ToLower(m)) })
}
