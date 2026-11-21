package deals

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// feature is a piece of equipment that makes a car nicer to own, with how
// much it counts. Cosmetic and universal items (spoilers, power steering, a
// trip computer) have no feature: they say nothing about how well a car is
// specced, and sources translate them to nothing.
type feature struct {
	weight int
	covers string // a lesser feature this one makes redundant (climate control covers A/C)
}

// features are the equipment vocabulary, by name.
var features = map[string]feature{
	"climate control":         {2, "air conditioning"},
	"air conditioning":        {1, ""},
	"adaptive cruise control": {2, "cruise control"},
	"cruise control":          {1, ""},
	"auxiliary heater":        {2, ""},
	"4x4":                     {2, ""},
	"360° cameras":            {2, "reversing camera"},
	"reversing camera":        {1, ""},
	"rear parking sensors":    {1, ""},
	"front parking sensors":   {1, ""},
	"self-parking":            {1, ""},
	"panoramic roof":          {2, "sunroof"},
	"sunroof":                 {1, ""},
	"keyless entry":           {1, ""},
	"rain sensor":             {1, ""},
	"xenon lights":            {1, ""},
	"alloy wheels":            {1, ""},
	"air suspension":          {1, ""},
	"electric tailgate":       {1, ""},
	"lane assist":             {1, ""},
	"emergency braking":       {1, ""},
	"blind-spot assist":       {1, ""},
	"sign recognition":        {1, ""},
	"distance warning":        {1, ""},
	"night vision":            {1, ""},
	"sport package":           {1, ""},
	"tow hook":                {1, ""},
}

// equipment is what a car has, by feature name, and its score.
type equipment struct {
	have    map[string]bool // with what better features imply (A/C under climate control)
	covered map[string]bool // implied, so not worth naming
	score   int
}

func equipmentOf(names []string) equipment {
	eq := equipment{have: map[string]bool{}}
	for _, n := range names {
		if _, ok := features[n]; ok {
			eq.have[n] = true
		}
	}
	covered := map[string]bool{}
	for n := range eq.have {
		if c := features[n].covers; c != "" {
			covered[c] = true
		}
	}
	for n := range eq.have {
		if !covered[n] {
			eq.score += features[n].weight
		}
	}
	for c := range covered { // climate control means it has A/C, for "missing"
		eq.have[c] = true
	}
	eq.covered = covered
	return eq
}

// Level is how well a car is equipped.
type Level int

const (
	Basic Level = iota
	Average
	WellEquipped
)

func (l Level) String() string {
	return [...]string{"👎 Basic", "😐 Average", "👍 Well equipped"}[l]
}

// minEquipmentPeers: below this many similar ads with known equipment, a car
// is judged against what cars of its class and age usually have instead.
const minEquipmentPeers = 5

// The equipment codebook: each make's or model's class, and what cars of a
// class and age usually have. Edit the JSON to correct or add cars.
//
//go:embed codebook/equipment.json
var equipmentJSON []byte

type equipmentBook struct {
	Classes  map[string]float64 `json:"classes"` // how much more than usual a class comes with
	Cars     []carClass         `json:"cars"`
	Expected []expected         `json:"expected"`
	Models   []trim             `json:"models"`
}

// trim is one model generation's range of equipment: what every one has
// (Base) and everything offered on the best-specced ones (Top, Base
// included).
type trim struct {
	Name   string   `json:"name"`
	Makes  []string `json:"makes"`
	Models []string `json:"models"` // prefixes, as in the reliability codebook
	Years  [2]int   `json:"years"`
	Base   []string `json:"base"`
	Top    []string `json:"top"`
}

// options are what a generation's better trims add over its base.
func (t trim) options() []string {
	var out []string
	for _, f := range t.Top {
		if !slices.Contains(t.Base, f) {
			out = append(out, f)
		}
	}
	return out
}

type carClass struct {
	Makes  []string `json:"makes"`
	Models []string `json:"models,omitempty"` // prefixes, as in the reliability codebook
	Class  string   `json:"class"`
}

type expected struct {
	Class    string   `json:"class"`
	Years    [2]int   `json:"years"`
	Features []string `json:"features"`
}

const defaultClass = "mainstream"

var loadEquipmentBook = sync.OnceValues(func() (*equipmentBook, error) {
	var b equipmentBook
	if err := json.Unmarshal(equipmentJSON, &b); err != nil {
		return nil, fmt.Errorf("equipment codebook: %w", err)
	}
	if _, ok := b.Classes[defaultClass]; !ok {
		return nil, fmt.Errorf("equipment codebook: no %q class", defaultClass)
	}
	for _, c := range b.Cars {
		if _, ok := b.Classes[c.Class]; !ok || len(c.Makes) == 0 {
			return nil, fmt.Errorf("equipment codebook: %v: unknown class %q or no makes", c.Makes, c.Class)
		}
	}
	for _, e := range b.Expected {
		if _, ok := b.Classes[e.Class]; !ok || e.Years[0] == 0 || e.Years[0] > e.Years[1] {
			return nil, fmt.Errorf("equipment codebook: expected %q %v: unknown class or bad years", e.Class, e.Years)
		}
		for _, f := range e.Features {
			if _, ok := features[f]; !ok {
				return nil, fmt.Errorf("equipment codebook: unknown feature %q", f)
			}
		}
	}
	for _, t := range b.Models {
		if t.Name == "" || len(t.Makes) == 0 || len(t.Models) == 0 || t.Years[0] == 0 || t.Years[0] > t.Years[1] {
			return nil, fmt.Errorf("equipment codebook: model %q: needs a name, makes, models and years", t.Name)
		}
		for _, f := range slices.Concat(t.Base, t.Top) {
			if _, ok := features[f]; !ok {
				return nil, fmt.Errorf("equipment codebook: model %q: unknown feature %q", t.Name, f)
			}
		}
		for _, f := range t.Base {
			if !slices.Contains(t.Top, f) {
				return nil, fmt.Errorf("equipment codebook: model %q: base %q missing from top", t.Name, f)
			}
		}
		if len(t.options()) == 0 {
			return nil, fmt.Errorf("equipment codebook: model %q: top adds nothing to base", t.Name)
		}
	}
	return &b, nil
})

// trimOf finds c's model generation, if the codebook has it. The longest
// matching model name wins: a "Discovery Sport" is no "Discovery".
func (b *equipmentBook) trimOf(c Car) (trim, bool) {
	model := strings.ToLower(c.Model)
	var best trim
	longest := 0
	for _, t := range b.Models {
		if c.Year < t.Years[0] || c.Year > t.Years[1] ||
			!slices.ContainsFunc(t.Makes, func(m string) bool { return strings.EqualFold(m, c.Make) }) {
			continue
		}
		for _, m := range t.Models {
			if strings.HasPrefix(model, strings.ToLower(m)) && len(m) > longest {
				best, longest = t, len(m)
			}
		}
	}
	return best, longest > 0
}

// classOf is c's class: the most specific entry naming its make (and model).
func (b *equipmentBook) classOf(c Car) string {
	class, best := defaultClass, -1
	model := strings.ToLower(c.Model)
	for _, e := range b.Cars {
		if !slices.ContainsFunc(e.Makes, func(m string) bool { return strings.EqualFold(m, c.Make) }) {
			continue
		}
		score := 0
		if len(e.Models) > 0 {
			if !slices.ContainsFunc(e.Models, func(m string) bool { return strings.HasPrefix(model, strings.ToLower(m)) }) {
				continue
			}
			score = 1
		}
		if score > best {
			class, best = e.Class, score
		}
	}
	return class
}

// expectedFor lists what a car of class from year usually has.
func (b *equipmentBook) expectedFor(class string, year int) []string {
	for _, e := range b.Expected {
		if e.Class == class && year >= e.Years[0] && year <= e.Years[1] {
			return e.Features
		}
	}
	return nil
}

// EquipmentRating says how well a car is equipped for what it is: against
// its model generation's trims when the codebook has them, else against
// similar ads, else against its class.
type EquipmentRating struct {
	Level   Level
	Class   string // budget, mainstream, premium or luxury
	Has     []string
	Missing []string // what cars like it usually have (or its better trims add) and this one lacks

	// Judged by trims.
	Trim       string   // e.g. "Volkswagen Golf 7"; "" = not judged by trims
	OptionsHad int      // of its generation's options over the base car
	Options    int      //
	Unticked   []string // base equipment the seller did not tick: the list may be incomplete

	// Judged against similar ads.
	Better int // similar ads it beats (ties count half, rounded down)
	Of     int // similar ads with known equipment; 0 = not judged this way
}

// Trim share thresholds: the weighted share of a generation's options a car
// has to count as near the top, or as a base car.
const (
	topTrimShare  = 0.6
	baseTrimShare = 0.2
)

// rateTrim places eq between t's base car and its fully loaded one.
func rateTrim(eq equipment, t trim) EquipmentRating {
	r := EquipmentRating{Trim: t.Name, Has: notable(eq)}
	var had, total int
	var lacking []string
	for _, f := range t.options() {
		total += features[f].weight
		r.Options++
		if eq.have[f] {
			had += features[f].weight
			r.OptionsHad++
		} else {
			lacking = append(lacking, f)
		}
	}
	slices.SortFunc(lacking, func(a, b string) int {
		return cmp.Or(cmp.Compare(features[b].weight, features[a].weight), cmp.Compare(a, b))
	})
	r.Missing = lacking[:min(4, len(lacking))]
	for _, f := range t.Base {
		if !eq.have[f] {
			r.Unticked = append(r.Unticked, f)
		}
	}
	switch share := float64(had) / float64(total); {
	case share >= topTrimShare:
		r.Level = WellEquipped
	case share <= baseTrimShare:
		r.Level = Basic
	default:
		r.Level = Average
	}
	return r
}

// RateEquipment rates c's equipment (only known once the ad page was read,
// and only when the seller ticked any) against peers of the same model.
// Without enough of those, it compares with what its class usually has at
// its age. Either way it names the usual features it lacks.
func RateEquipment(c Car, peers []Peer) (EquipmentRating, bool) {
	if len(c.Equipment) == 0 {
		return EquipmentRating{}, false
	}
	book, err := loadEquipmentBook()
	if err != nil {
		return EquipmentRating{}, false // a broken codebook fails its test; alerts still go out
	}
	eq := equipmentOf(c.Equipment)
	if t, ok := book.trimOf(c); ok {
		return rateTrim(eq, t), true
	}
	class := book.classOf(c)
	r := EquipmentRating{Class: class, Has: notable(eq)}
	for _, f := range book.expectedFor(class, c.Year) {
		if !eq.have[f] {
			r.Missing = append(r.Missing, f)
		}
	}

	var scores []int
	for _, p := range peers {
		if len(p.Equipment) > 0 {
			scores = append(scores, equipmentOf(p.Equipment).score)
		}
	}
	if len(scores) >= minEquipmentPeers {
		half := 0
		for _, s := range scores {
			switch {
			case eq.score > s:
				r.Better++
			case eq.score == s:
				half++
			}
		}
		r.Better += half / 2
		r.Of = len(scores)
		switch share := float64(r.Better) / float64(r.Of); {
		case share >= 2.0/3:
			r.Level = WellEquipped
		case share < 1.0/3:
			r.Level = Basic
		default:
			r.Level = Average
		}
		return r, true
	}

	usual := float64(usualScore(c.Year)) * book.Classes[class]
	switch {
	case float64(eq.score) >= usual*1.35:
		r.Level = WellEquipped
	case float64(eq.score) <= usual*0.65 || len(r.Missing) > 2:
		r.Level = Basic
	default:
		r.Level = Average
	}
	return r, true
}

// usualScore is roughly what a mainstream car of that model year has: newer
// cars come with more as standard. A class scales it.
func usualScore(year int) int {
	switch {
	case year < 2005:
		return 3
	case year < 2010:
		return 5
	case year < 2015:
		return 7
	case year < 2020:
		return 9
	}
	return 11
}

// notable lists the most valuable features a car has, at most six.
func notable(eq equipment) []string {
	var names []string
	for n := range eq.have {
		if !eq.covered[n] {
			names = append(names, n)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(features[b].weight, features[a].weight), cmp.Compare(a, b))
	})
	return names[:min(6, len(names))]
}

// Text is the equipment line of an alert, e.g. "👍 Well equipped: better
// than 7 of 9 similar ads. Has climate control, 4x4, ... No reversing camera,
// which premium cars of its age usually have."
func (r EquipmentRating) Text() string {
	if r.Trim != "" {
		return r.trimText()
	}
	s := r.Level.String()
	if r.Of > 0 {
		s += fmt.Sprintf(": better than %d of %d similar ads", r.Better, r.Of)
	} else {
		s += fmt.Sprintf(" for a %s car of its age (too few similar ads with known equipment to compare)", r.Class)
	}
	s += "."
	if len(r.Has) > 0 {
		s += " Has " + strings.Join(r.Has, ", ") + "."
	}
	if len(r.Missing) > 0 {
		s += fmt.Sprintf(" No %s, which %s cars of its age usually have.", strings.Join(r.Missing, ", no "), r.Class)
	}
	return s
}

var trimLabel = [...]string{"👎 Base trim", "😐 Mid trim", "👍 Near top trim"}

// trimText is e.g. "👍 Near top trim: has 11 of the 17 options a Volkswagen
// Golf 7 could have. Has ... Lacks panoramic roof, 4x4."
func (r EquipmentRating) trimText() string {
	s := fmt.Sprintf("%s: has %d of the %d options a %s could have.", trimLabel[r.Level], r.OptionsHad, r.Options, r.Trim)
	if len(r.Has) > 0 {
		s += " Has " + strings.Join(r.Has, ", ") + "."
	}
	if len(r.Missing) > 0 {
		s += " Lacks " + strings.Join(r.Missing, ", ") + "."
	}
	if len(r.Unticked) > 0 {
		s += fmt.Sprintf(" The seller did not tick %s, which every one has, so the list may be incomplete.", strings.Join(r.Unticked, ", "))
	}
	return s
}
