package rrr

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// CategoryParts is the one thing rrr.lt sells: used car parts.
const CategoryParts = "parts"

// KeywordsKey is the filter of part numbers and free words ("8V0941005",
// "xenon"), any of which the title or TextAttr must contain. The part (OEM)
// number is how parts are usually looked for.
const KeywordsKey = "code"

type option struct{ ID, Label string }

// The filters are what tells one part from another, judged by the site's own
// facet counts (2026-11, 49M parts): condition is always set and 6% of
// headlights are "with defects"; the side is set on 59% of headlights and is
// a different part. Left out: the donor car's body type (55-60% "other"),
// fuel (missing on 22-35%, and meaningless for most parts) and gearbox (the
// part category already says "Automatic gearbox"); filtering on them would
// hide parts more than find them.
var qualityOptions = []option{
	{"USED", "Used"}, {"NEW", "New"}, {"WITH_DEFECTS", "With defects"}, {"REFURBISHED", "Refurbished"},
}

// sideOptions group the API's positions: sellers mark the same left
// headlight LEFT or LEFT_FRONT, so "Left" takes both.
type sideOption struct {
	Label string
	IDs   []string
}

var sideOptions = []sideOption{
	{"Left", []string{"LEFT", "LEFT_FRONT", "LEFT_REAR"}},
	{"Right", []string{"RIGHT", "RIGHT_FRONT", "RIGHT_REAR"}},
	{"Front", []string{"FRONT", "LEFT_FRONT", "RIGHT_FRONT"}},
	{"Rear", []string{"REAR", "LEFT_REAR", "RIGHT_REAR"}},
	{"Centre", []string{"CENTER"}},
	{"Set or pair", []string{"SET", "ALL"}},
}

// sideNames shows a part's own position.
var sideNames = map[string]string{
	"LEFT": "Left", "RIGHT": "Right", "FRONT": "Front", "REAR": "Rear", "CENTER": "Centre",
	"LEFT_FRONT": "Left front", "RIGHT_FRONT": "Right front", "LEFT_REAR": "Left rear", "RIGHT_REAR": "Right rear",
	"SET": "Set", "ALL": "Set",
}

// Display names of the donor car's facts in a notification (not filters).
var (
	fuelNames = map[string]string{
		"DIESEL": "Diesel", "GASOLINE": "Petrol", "GASOLINE_GAS": "Petrol/gas", "GASOLINE_HYBRID": "Petrol hybrid",
		"DIESEL_HYBRID": "Diesel hybrid", "DIESEL_GAS": "Diesel/gas", "ELECTRIC": "Electric", "BIOETHANOL": "Bioethanol",
	}
	gearboxNames = map[string]string{"MANUAL": "Manual", "AUTOMATIC": "Automatic"}
)

func labels(os []option) []string {
	out := make([]string, len(os))
	for i, o := range os {
		out[i] = o.Label
	}
	return out
}

func labelOf(os []option, id string) string {
	for _, o := range os {
		if o.ID == id {
			return o.Label
		}
	}
	return ""
}

// lookupOption takes a label or an id, ignoring case.
func lookupOption(os []option, name string) (option, bool) {
	for _, o := range os {
		if textutil.Normalize(o.Label) == textutil.Normalize(name) || strings.EqualFold(o.ID, strings.TrimSpace(name)) {
			return o, true
		}
	}
	return option{}, false
}

var (
	categoriesOnce sync.Once
	categories     []sources.Category
)

// Categories implements sources.Catalog: the part tree and the car models
// as one form, for the dashboard's editor.
func (*Client) Categories() []sources.Category {
	categoriesOnce.Do(func() {
		cb, err := PartsCodebook()
		if err != nil {
			slog.Error("codebook", "source", Source, "err", err)
			return
		}
		models := map[string][]string{}
		for _, m := range cb.Makes {
			if len(m.labels) > 0 {
				models[m.Name] = m.labels
			}
		}
		// Every rrr.lt ad is a part (Location ""), its feed shows it all.
		categories = []sources.Category{{ID: CategoryParts, Label: "Car parts", Fields: []sources.FieldSpec{
			{Key: "part", Label: "Part", Kind: "multi", Options: cb.PartOptions()},
			{Key: "make", Label: "Make", Kind: "multi", Options: cb.makeNames()},
			{Key: "model", Label: "Model", Kind: "multi", DependsOn: "make", OptionsBy: models},
			{Key: "year", Label: "Car year", Kind: "range"},
			{Key: "side", Label: "Side", Kind: "multi", Options: sideLabels()},
			{Key: KeywordsKey, Label: "Part number or words", Kind: "text"},
			{Key: "price", Label: "Price, €", Kind: "range"},
			{Key: "quality", Label: "Condition", Kind: "multi", Options: labels(qualityOptions)},
		}}}
	})
	return categories
}

var filterKeys = []string{"part", "make", "model", "year", "side", KeywordsKey, "price", "quality"}

func sideLabels() []string {
	out := make([]string, len(sideOptions))
	for i, o := range sideOptions {
		out[i] = o.Label
	}
	return out
}

// CompileSearch implements sources.Catalog. Everything but keywords goes
// into the feed, so the site does the filtering:
//
//	part: ["Engine › Turbocharger"]   make: [BMW]   model: ["3 E46 (1998–2005)"]
//	year: {min: 2000}   side: [Left]   price: {max: 300}   quality: [Used]
//	code: ["11657790"]
//
// A part names a category path or, when only one category has it, a bare
// name ("Turbo"). Names come back spelled as in the codebook.
func (*Client) CompileSearch(category string, filters map[string]any) (sources.Compiled, error) {
	if category != CategoryParts {
		return sources.Compiled{}, &sources.InvalidSearch{Msg: fmt.Sprintf("unknown category %q; choose %s", category, CategoryParts)}
	}
	cb, err := PartsCodebook()
	if err != nil {
		return sources.Compiled{}, err
	}
	c, err := cb.compile(filters)
	if err != nil {
		var inv *sources.InvalidSearch
		if !errors.As(err, &inv) {
			err = &sources.InvalidSearch{Msg: err.Error()}
		}
		return sources.Compiled{}, err
	}
	return c, nil
}

func (cb *Codebook) compile(in map[string]any) (sources.Compiled, error) {
	for k := range in {
		if !slices.Contains(filterKeys, k) {
			return sources.Compiled{}, fmt.Errorf("unknown filter %q%s; valid: %s", k, didYouMean(k, filterKeys), strings.Join(filterKeys, ", "))
		}
	}
	var f feedSpec
	spec, filters := map[string]any{}, map[string]any{}

	parts, err := stringList("part", in["part"])
	if err != nil {
		return sources.Compiled{}, err
	}
	var shownParts []string
	for _, n := range parts {
		cs, err := cb.part(n)
		if err != nil {
			return sources.Compiled{}, err
		}
		if !slices.Contains(shownParts, cs[0].Path) {
			shownParts = append(shownParts, cs[0].Path)
		}
		for _, c := range cs {
			if !slices.Contains(f.Cats, c.ID) {
				f.Cats = append(f.Cats, c.ID)
			}
		}
	}
	// A category chosen with one of its ancestors adds nothing.
	f.Cats = slices.DeleteFunc(f.Cats, func(id int) bool {
		return slices.ContainsFunc(cb.Ancestors(id)[1:], func(a int) bool { return slices.Contains(f.Cats, a) })
	})
	if len(shownParts) > 0 {
		spec["part"] = textutil.ToAny(shownParts)
	}

	makeNames, err := stringList("make", in["make"])
	if err != nil {
		return sources.Compiled{}, err
	}
	var makes []*Make
	for _, n := range makeNames {
		m, ok := cb.MakeByName(n)
		if !ok {
			return sources.Compiled{}, fmt.Errorf("unknown make %q%s", n, didYouMean(n, cb.makeNames()))
		}
		if !slices.Contains(makes, m) {
			makes = append(makes, m)
		}
	}
	modelNames, err := stringList("model", in["model"])
	if err != nil {
		return sources.Compiled{}, err
	}
	if len(modelNames) > 0 && len(makes) == 0 {
		return sources.Compiled{}, errors.New("choose the make of the models first")
	}
	var shownModels []string
	withModel := map[int]bool{}
	for _, n := range modelNames {
		found := false
		var all []string
		for _, m := range makes {
			if o, ok := m.Model(n); ok {
				f.Models = append(f.Models, [2]int{m.ID, o.ID})
				shownModels = append(shownModels, o.Label)
				withModel[m.ID], found = true, true
				break
			}
			all = append(all, m.labels...)
		}
		if !found {
			return sources.Compiled{}, fmt.Errorf("unknown model %q for the chosen makes%s", n, didYouMean(n, all))
		}
	}
	var shownMakes []string
	for _, m := range makes {
		shownMakes = append(shownMakes, m.Name)
		if !withModel[m.ID] { // a make with models chosen is only those models
			f.Makes = append(f.Makes, m.ID)
		}
	}
	if len(shownMakes) > 0 {
		spec["make"] = textutil.ToAny(shownMakes)
	}
	if len(shownModels) > 0 {
		spec["model"] = textutil.ToAny(textutil.Uniq(shownModels))
	}
	if len(f.Cats) == 0 && len(makes) == 0 {
		return sources.Compiled{}, errors.New("choose a part or a car make: rrr.lt has too many new parts to watch them all")
	}

	for _, r := range []struct {
		key, label string
		out        *span
	}{{"year", "Car year", &f.Year}, {"price", "Price", &f.Price}} {
		v, ok := in[r.key]
		if !ok || v == nil {
			continue
		}
		s, shown, err := rangeOf(r.label, v)
		if err != nil {
			return sources.Compiled{}, err
		}
		if !s.empty() {
			*r.out, spec[r.key] = s, shown
		}
	}

	qualities, err := stringList("quality", in["quality"])
	if err != nil {
		return sources.Compiled{}, err
	}
	var shownQuality []string
	for _, n := range qualities {
		o, ok := lookupOption(qualityOptions, n)
		if !ok {
			return sources.Compiled{}, fmt.Errorf("condition: unknown value %q%s; valid: %s", n, didYouMean(n, labels(qualityOptions)), strings.Join(labels(qualityOptions), ", "))
		}
		if !slices.Contains(f.Quality, o.ID) {
			f.Quality = append(f.Quality, o.ID)
			shownQuality = append(shownQuality, o.Label)
		}
	}
	if len(shownQuality) > 0 {
		spec["quality"] = textutil.ToAny(shownQuality)
	}

	sides, err := stringList("side", in["side"])
	if err != nil {
		return sources.Compiled{}, err
	}
	var shownSides []string
	for _, n := range sides {
		i := slices.IndexFunc(sideOptions, func(o sideOption) bool { return textutil.Normalize(o.Label) == textutil.Normalize(n) })
		if i < 0 {
			return sources.Compiled{}, fmt.Errorf("side: unknown value %q%s; valid: %s", n, didYouMean(n, sideLabels()), strings.Join(sideLabels(), ", "))
		}
		if !slices.Contains(shownSides, sideOptions[i].Label) {
			shownSides = append(shownSides, sideOptions[i].Label)
			f.Side = append(f.Side, sideOptions[i].IDs...)
		}
	}
	if len(shownSides) > 0 {
		spec["side"] = textutil.ToAny(shownSides)
	}

	words, err := keywords(in[KeywordsKey])
	if err != nil {
		return sources.Compiled{}, err
	}
	if len(words) > 0 {
		filters[TextAttr] = map[string]any{"contains": textutil.ToAny(words)}
		spec[KeywordsKey] = textutil.ToAny(words)
	}

	return sources.Compiled{Spec: spec, Feeds: []string{f.String()}, Filters: filters}, nil
}

// part finds the categories of a path ("Engine › Turbocharger"), or of a
// name only one path ends in ("Turbo"). Two categories can share a path;
// both are searched.
func (cb *Codebook) part(name string) ([]*Category, error) {
	if cs, ok := cb.byPath[pathKey(name)]; ok {
		return cs, nil
	}
	switch paths := cb.byLeaf[textutil.Normalize(name)]; len(paths) {
	case 1:
		return cb.byPath[pathKey(paths[0])], nil
	case 0:
		var near []string
		for _, leaf := range textutil.ClosestContaining(name, cb.leaves) {
			near = append(near, cb.byLeaf[textutil.Normalize(leaf)]...)
		}
		if len(near) > 0 {
			return nil, fmt.Errorf("unknown part %q (did you mean %s?)", name, strings.Join(textutil.QuoteAll(near[:min(len(near), 3)]), " or "))
		}
		return nil, fmt.Errorf("unknown part %q; pick one from the list", name)
	default:
		return nil, fmt.Errorf("%q is in several groups; write one of %s", name, strings.Join(textutil.QuoteAll(paths), ", "))
	}
}

// rangeOf reads {min, max} as whole numbers; either may be missing.
func rangeOf(label string, v any) (span, map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return span{}, nil, fmt.Errorf("%s: give a range {min, max}", label)
	}
	var s span
	shown := map[string]any{}
	for k, b := range m {
		if k != "min" && k != "max" {
			return span{}, nil, fmt.Errorf("%s: a range has only min and max, not %q", label, k)
		}
		if b == nil {
			continue
		}
		n, ok := textutil.Number(b)
		if !ok || n < 0 || n > 1e9 {
			return span{}, nil, fmt.Errorf("%s: %s must be a number, got %v", label, k, b)
		}
		i := int(math.Round(n))
		if k == "min" {
			s.Min = &i
		} else {
			s.Max = &i
		}
		shown[k] = float64(i)
	}
	if s.Min != nil && s.Max != nil && *s.Min > *s.Max {
		return span{}, nil, fmt.Errorf("%s: min %d is above max %d", label, *s.Min, *s.Max)
	}
	return s, shown, nil
}

// stringList reads a filter value written as one string or a list.
func stringList(key string, v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s := strings.TrimSpace(fmt.Sprint(e)); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case []string:
		return t, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("filter %q: expected a name or a list of names, got %v", key, v)
}

// keywords reads the keywords filter: trimmed, without repeats.
func keywords(v any) ([]string, error) {
	names, err := stringList(KeywordsKey, v)
	if err != nil {
		return nil, err
	}
	return textutil.Keywords(names)
}

// didYouMean also suggests the names containing s: part paths are long.
func didYouMean(s string, names []string) string {
	return textutil.DidYouMean(textutil.ClosestContaining(s, names))
}
