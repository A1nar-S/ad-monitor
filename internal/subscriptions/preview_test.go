package subscriptions

import (
	"slices"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/rrr"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/ss"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

func TestPreview(t *testing.T) {
	svc := NewService(nil, sources.NewRegistry(ss.NewClient(0)), 3)
	car := func(location string, attrs map[string]any) events.ListingDiscovered {
		return events.ListingDiscovered{Source: "ss.com", ExternalID: location, Location: location, Attrs: attrs}
	}
	candidates := []events.ListingDiscovered{
		0: car("transport/cars/audi/a4", map[string]any{"make": "Audi", "year": 2010.0}),
		1: car("transport/cars/audi/a6", map[string]any{"make": "Audi", "year": 2001.0}),                       // too old
		2: car("transport/cars/bmw/x3", map[string]any{"make": "BMW", "year": 2015.0}),                         // other make
		3: car("transport/cars/audi/a4", map[string]any{"make": "Audi", "year": 2012.0, "gearbox": "Manuāla"}), // page read: manual
		4: car("transport/cars/audi/a4", map[string]any{"make": "Audi", "year": 2012.0, "gearbox": "Automāts"}),
	}

	p, err := svc.Preview(SearchInput{Category: "cars", Filters: map[string]any{
		"make": []any{"Audi"}, "year": map[string]any{"min": 2005.0}, "gearbox": []any{"Automāts"},
	}}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	// Car searches share the one transport/cars feed, so every car is in
	// scope and the make is a filter.
	if p.InScope != 5 {
		t.Errorf("in scope = %d, want all 5 cars", p.InScope)
	}
	if !slices.Equal(p.Matched, []int{4}) {
		t.Errorf("matched = %v, want only the automatic whose page was read", p.Matched)
	}
	if !slices.Equal(p.Unsure, []int{0}) || !slices.Equal(p.UnsureKeys, []string{"gearbox"}) {
		t.Errorf("unsure = %v %v, want the 2010 A4 pending its gearbox", p.Unsure, p.UnsureKeys)
	}

	if _, err := svc.Preview(SearchInput{Category: "boats"}, candidates); err == nil {
		t.Error("unknown category: want an error")
	}
}

// An rrr.lt search runs through the same compile and match path: its feed
// carries the filters, and parts seen on another search's feed are scoped by
// their attrs (a category includes its subcategories); part numbers stay local.
func TestPreviewRRR(t *testing.T) {
	svc := NewService(nil, sources.NewRegistry(ss.NewClient(0), rrr.NewClient(0)), 3)
	part := func(id, category, make, model string, year float64, title string) events.ListingDiscovered {
		return events.ListingDiscovered{
			Source: rrr.Source, ExternalID: id, Feed: "cat=1", Title: title,
			Attrs: map[string]any{"category_id": category, "make_id": make, "model_id": model, "year": year},
		}
	}
	candidates := []events.ListingDiscovered{
		0: part("1", "230", "1", "1945", 2022, "Coolant expansion tank/reservoir · BMW X3 G01, 2022 · 7925685"),
		1: part("2", "230", "1", "1945", 2016, "Coolant expansion tank/reservoir · BMW X3 G01, 2016 · 7925685"), // too old
		2: part("3", "205", "1", "1945", 2022, "A/C cooling radiator · BMW X3 G01, 2022 · 7925685"),             // other part
		3: part("4", "230", "1", "1945", 2022, "Coolant expansion tank/reservoir · BMW X3 G01, 2022 · 1234"),    // no keyword
	}
	p, err := svc.Preview(SearchInput{Source: rrr.Source, Category: rrr.CategoryParts, Filters: map[string]any{
		"part": []any{"Air conditioning-heating system/radiators › Radiators/fans"},
		"make": []any{"BMW"}, "model": []any{"X3 G01"}, "year": map[string]any{"min": 2018.0},
		"code": []any{"7925685"},
	}}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if p.InScope != 2 || !slices.Equal(p.Matched, []int{0}) || len(p.Unsure) != 0 {
		t.Errorf("preview = %+v, want 2 in scope and only the 2022 part with the number matched", p)
	}

	// An ss.com category is not an rrr.lt one.
	if _, err := svc.Preview(SearchInput{Source: rrr.Source, Category: "cars"}, candidates); err == nil {
		t.Error("cars on rrr.lt: want an error")
	}
}
