// Package sources defines what a listing site must provide to plug into the
// platform. The services only know this interface: adding city24, vinted, ...
// means writing one implementation under internal/scraping/<site> and
// registering it in app.KnownSources.
package sources

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

// Source is one listing site.
type Source interface {
	// Name is the value of events.ListingDiscovered.Source.
	Name() string

	// FetchFeed reads one page of a feed, newest first. A feed is an opaque,
	// source-defined string (an SS.com category path, a vinted search, ...).
	FetchFeed(ctx context.Context, feed string, page int) ([]events.ListingDiscovered, error)

	// PlanFeeds turns the feeds searches asked for into the feeds to fetch,
	// merging where one broader request is cheaper than several narrow ones.
	// Only the site knows what is cheaper, hence it lives here.
	PlanFeeds(wanted []string, collapseAt int) []string

	// InScope reports whether a listing belongs to what a search asked for.
	// It is needed because a planned feed can be broader than any one search.
	InScope(wanted []string, l events.ListingDiscovered) bool
}

// Catalog is optionally implemented by a source whose searches are written
// in its own vocabulary (ss.com: the search forms' fields and option names).
// Users pick a category and fill in filters; they never see feeds.
type Catalog interface {
	// Categories describes what can be searched, for the bot's autocomplete
	// and the dashboard's forms.
	Categories() []Category
	// CompileSearch validates a search and returns it in canonical spelling
	// plus the feeds to watch and the filters to match. Unknown fields or
	// values are an *InvalidSearch with "did you mean" suggestions.
	CompileSearch(category string, filters map[string]any) (Compiled, error)
}

// Compiled is a validated search.
type Compiled struct {
	// Filters as the user should see them again (canonical names), e.g.
	// {"make": ["Skoda"], "region": ["Rīga"]}.
	Spec map[string]any
	// Feeds to scrape and Filters to match: what the pipeline runs on.
	Feeds   []string
	Filters map[string]any
}

// InvalidSearch is a mistake in what the user wrote; its message is meant
// for them.
type InvalidSearch struct{ Msg string }

func (e *InvalidSearch) Error() string { return e.Msg }

// Category is one searchable kind of listing, e.g. cars.
type Category struct {
	ID     string      `json:"id"` // "cars", "flats"
	Label  string      `json:"label"`
	Fields []FieldSpec `json:"fields"`
	// Location is where the category's ads are: their
	// events.ListingDiscovered.Location is it or starts with it and "/";
	// "" = every ad of the source. AdPage: their ad pages add facts (see
	// Enricher). The admin overview sorts stored ads by these.
	Location string `json:"-"`
	AdPage   bool   `json:"-"`
}

// FieldSpec describes one filter of a category.
type FieldSpec struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind: "multi" (any of Options), "all" (must have all of Options),
	// "range" ({min, max}), "text" (free words, any of which the ad's text
	// must contain).
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	// Detail: only the ad page has it, so it is checked last.
	Detail  bool     `json:"detail,omitempty"`
	Options []string `json:"options,omitempty"`
	// DependsOn names the field whose chosen values pick the options here
	// (a make's models, a region's districts); OptionsBy holds them.
	DependsOn string              `json:"dependsOn,omitempty"`
	OptionsBy map[string][]string `json:"optionsBy,omitempty"`
}

// Enricher is optionally implemented by a source whose feed pages lack some
// facts that its ad pages have (ss.com cars: gearbox, body, color, ...).
// The matcher calls Enrich only for a listing that passes every filter it can
// check without them, and at most once per listing, whoever is watching.
type Enricher interface {
	// EnrichableKeys are the attrs Enrich can add.
	EnrichableKeys() []string
	// Enrich reads the ad page and returns the listing with those attrs
	// added. ErrGone means the ad no longer exists.
	Enrich(ctx context.Context, l events.ListingDiscovered) (events.ListingDiscovered, error)
}

// CarAds is optionally implemented by a source that lists whole cars (ss.com).
// The matcher rates such ads for the alert: overall, price and equipment
// against similar stored ones, engine and gearbox reputation (see
// internal/deals). Car parts carry a make and model too, which is why the
// source must say which ads are cars.
type CarAds interface {
	// RateableCar reports whether l is a car offered for sale that can be
	// compared with others (not one filed under a catch-all "other" model).
	// It is also asked of stored ads, which carry only Source, Feed and
	// Location.
	RateableCar(l events.ListingDiscovered) bool
	// CarTerm translates one of the source's attr values into the English
	// vocabulary internal/deals rates in: for "fuel" petrol, diesel, hybrid
	// or electric; for "gearbox" automatic or manual; for "equipment" a
	// feature name such as "climate control". "" = no equivalent. The site's
	// own words stay in its adapter.
	CarTerm(key, value string) string
}

// Scannable is optionally implemented by a source whose every listing of a
// category can be read at once and rated (the gem finder, internal/gems).
type Scannable interface {
	// ScanScope says where category's listings are; ok is false when the
	// source has no such category to scan.
	ScanScope(category string) (scope ScanScope, ok bool)
}

// ScanScope is where a scan finds one category's listings.
type ScanScope struct {
	// Feed lists every one of them that is for sale.
	Feed string
	// Location is the prefix of the stored ones' Location: those a scan
	// rates and compares with (the category decides which count).
	Location string
}

// ErrGone is returned by Enrich for an ad that was removed meanwhile.
var ErrGone = errors.New("listing no longer exists")

// Registry holds the sources a process knows about. The first one given
// to NewRegistry is the default: the site a search watches unless it names
// another (the bot's searches, the dashboard's first tab).
type Registry struct {
	byName map[string]Source
	first  string
}

func NewRegistry(srcs ...Source) Registry {
	r := Registry{byName: make(map[string]Source, len(srcs))}
	for _, s := range srcs {
		r.byName[s.Name()] = s
	}
	if len(srcs) > 0 {
		r.first = srcs[0].Name()
	}
	return r
}

func (r Registry) Get(name string) (Source, error) {
	if s, ok := r.byName[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown source %q", name)
}

// Lookup returns the source called name, or nil.
func (r Registry) Lookup(name string) Source { return r.byName[name] }

// Default names the default source; "" in an empty registry.
func (r Registry) Default() string { return r.first }

// Names lists the registered sources, sorted.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
