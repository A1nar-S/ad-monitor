package gems

import (
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// A Category is one kind of listing the finder rates (cars: internal/gems/cars).
// This package scans, stores, filters and lists; the category says which
// stored listings count, how good a buy each is, which deserve their ad page
// read, and what people can filter on.
type Category interface {
	// ID is the sources' category ID, e.g. "cars".
	ID() string
	Spec() Spec
	// Rater rates items, the stored listings of src in the scan's scope;
	// ok is false when src cannot be rated for this category.
	Rater(src sources.Source, items []Item) (r Rater, ok bool)
	// Belongs reports whether a stored listing of src counts as one of the
	// category's for sale. It is also asked of ads with only Source, Feed
	// and Location, to mark the sold ones.
	Belongs(src sources.Source, l events.ListingDiscovered) bool
}

// Item is a stored listing a scan rates and compares with.
type Item struct {
	Listing  events.ListingDiscovered // ad-page details merged into Attrs
	Active   bool
	Detailed bool // its ad page was read
}

// Rater rates one scan's items.
type Rater interface {
	// Rate rates every active item it can say something about, best first.
	Rate() []Rated
	// Update replaces item i: its ad page was read, or it was removed.
	Update(i int, item Item)
}

// Rated is the verdict on one item.
type Rated struct {
	Item    int     // index into the items
	Score   float64 // the rank, highest first; -Inf = too little known, last
	Display int     // 0-100, for people
	Buy     string  // good, average, bad or check (too cheap to trust)
	Heading string  // what it is, e.g. "BMW 520"
	Overall string  // the verdict line
	Fields  []events.Field
	// Components are the score's parts, for the breakdown.
	Components []Component
	PriceDiff  *float64 // vs similar listings: -0.14 is 14% below; nil = not compared
	// Candidate: worth reading its ad page for what the list lacks.
	Candidate bool
	// Attrs are the values the filters and cards show, keyed as the
	// category's Spec: numbers float64, the rest strings; unknown left out.
	Attrs map[string]any
	// Translations are Overall, Fields and the components' Why in other
	// languages than English, by language code ("lv").
	Translations map[string]Text
}

// Text is what a rated listing says, in one language.
type Text struct {
	Overall string            `json:"overall"`
	Fields  []events.Field    `json:"fields"`
	Why     map[string]string `json:"why,omitempty"` // by component key
}

// Component is one part of a score: x from -1 to +1, weighted.
type Component struct {
	Key    string  `json:"key"`
	X      float64 `json:"x"`
	Weight float64 `json:"weight"`
	Points float64 `json:"points"` // Weight × X
	// Score is Points on the 0–100 scale: what it adds to or takes from
	// the neutral 50.
	Score float64 `json:"score"`
	Why   string  `json:"why"`
}

// Spec is what people can do with a category's gems.
type Spec struct {
	Fields []Field `json:"fields"`
	Sorts  []Sort  `json:"sorts"`
}

// Field is one attr the gems carry, as a filter and on the cards.
type Field struct {
	Key string `json:"key"`
	// Kind: choice (any of the values there are) or range (numbers, from
	// and to).
	Kind string `json:"kind"`
	// Control, for a choice: select (one, from a list), chips (any, toggled)
	// or search (any, typed). For a range: range (from and to) or max (up to).
	Control string `json:"control"`
	// DependsOn names the choice this one narrows (a make's models).
	DependsOn string `json:"dependsOn,omitempty"`
	// Detail: only the ad page has it, so only read listings do.
	Detail bool `json:"detail,omitempty"`
	// Unit formats numbers on the cards: km, l, or "" (as is, e.g. a year).
	Unit string `json:"unit,omitempty"`
	// Card: shown among a gem's specs.
	Card bool `json:"card,omitempty"`
	// Tones colour some values on the cards: good or bad.
	Tones map[string]string `json:"tones,omitempty"`
	// Icon marks the value on the cards: pin (a place), or "" for none.
	Icon string `json:"icon,omitempty"`
	// Catalog: the values to offer are the source's search catalog's for
	// the same key (sources.Catalog), not only those the gems have; the
	// API fills Options from it.
	Catalog bool     `json:"-"`
	Options []string `json:"options,omitempty"`
}

// Field kinds and controls.
const (
	Choice = "choice"
	Range  = "range"
)

// Sort is an order a category offers beyond the common ones (best, price,
// discount): by a numeric attr, unknown (or 0) last.
type Sort struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Desc bool   `json:"desc,omitempty"`
}

func (s Spec) field(key string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}
