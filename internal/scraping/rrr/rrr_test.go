package rrr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// fixtureFeed is the query testdata/page_bmw_ac.json was fetched with: BMW
// parts in Air conditioning-heating system/radiators, newest first.
const fixtureFeed = "cat=197&make=1"

func fixturePage(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/page_bmw_ac.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fakeAPI serves the fixture and records the variables of each query.
func fakeAPI(t *testing.T, status int) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	page := fixturePage(t)
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		var req gqlRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		got = append(got, req.Variables)
		w.WriteHeader(status)
		_, _ = w.Write(page)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func fetchFixture(t *testing.T) []events.ListingDiscovered {
	t.Helper()
	srv, _ := fakeAPI(t, http.StatusOK)
	ls, err := NewClient(5*time.Second).WithBaseURL(srv.URL).FetchFeed(context.Background(), fixtureFeed, 1)
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func TestFetchFeedSendsTheFeedAsQuery(t *testing.T) {
	srv, got := fakeAPI(t, http.StatusOK)
	c := NewClient(5 * time.Second).WithBaseURL(srv.URL)
	feed := "cat=197&model=1:1945&year=2018-&price=-100&quality=USED&side=LEFT,LEFT_FRONT"
	for page := 1; page <= 2; page++ {
		if _, err := c.FetchFeed(context.Background(), feed, page); err != nil {
			t.Fatal(err)
		}
	}
	if len(*got) != 2 {
		t.Fatalf("got %d requests, want 2", len(*got))
	}
	first, second := (*got)[0], (*got)[1]
	if _, ok := first["after"]; ok {
		t.Errorf("page 1 sent a cursor: %v", first["after"])
	}
	if second["after"] != cursor(2) {
		t.Errorf("page 2 cursor = %v, want %v", second["after"], cursor(2))
	}
	in, _ := json.Marshal(first["input"])
	want := `{"categoryIds":["197"],"modelYear":{"from":2018},"partPositionIds":["LEFT","LEFT_FRONT"],"price":{"to":100},` +
		`"qualityIds":["USED"],"sort":[{"direction":"DESC","property":"CREATED_AT"}],` +
		`"vehicleModels":[{"manufacturerId":"1","modelId":"1945"}]}`
	if string(in) != want {
		t.Errorf("input =\n%s\nwant\n%s", in, want)
	}
	if first["first"] != float64(pageSize) {
		t.Errorf("first = %v, want %d", first["first"], pageSize)
	}
}

func TestCursorIsTheOffsetBeforeThePage(t *testing.T) {
	for page, want := range map[int]string{2: "arrayconnection:14", 3: "arrayconnection:29"} {
		raw, err := base64.StdEncoding.DecodeString(cursor(page))
		if err != nil || string(raw) != want {
			t.Errorf("cursor(%d) = %q (%v), want %q", page, raw, err, want)
		}
	}
}

func TestListingFromAPI(t *testing.T) {
	ls := fetchFixture(t)
	if len(ls) != 15 {
		t.Fatalf("got %d listings, want 15", len(ls))
	}
	l := ls[0]
	price := 94.12
	want := events.ListingDiscovered{
		Source:     Source,
		ExternalID: "118816724",
		Feed:       fixtureFeed,
		Title:      "Coolant expansion tank/reservoir · BMW X3 G01, 2022",
		URL:        "https://rrr.lt/en/used-part/dwz14292-bmw-x3-g01-coolant-expansion-tank-reservoir",
		ImageURL:   imageBase + "/dwz/0/0/0/0/1/4/2/9/2/7740a803a17309f2d567d4ad39bfea05-bmw_x3_g01_coolant_expansion_tank_reservoir.jpg",
		Price:      "94.12 €",
		PriceValue: &price,
		PostedAt:   time.Date(2026, 11, 21, 19, 21, 22, 0, time.UTC),
		Attrs: map[string]any{
			attrCategoryID: "230", attrCategory: "Coolant expansion tank/reservoir", attrQuality: "USED",
			attrMakeID: "1", attrMake: "BMW", attrModelID: "1945", attrModel: "X3 G01",
			attrYear: float64(2022), attrGearbox: "AUTOMATIC",
			TextAttr: "7925685 8742660 8610661 BMW X3 G01 (18 - 24) 2022",
		},
	}
	got := l
	got.Fields = nil
	got.PostedAt = got.PostedAt.UTC()
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("listing =\n%s\nwant\n%s", gj, wj)
	}
	if len(l.Fields) == 0 || l.Fields[0].Value != "Coolant expansion tank/reservoir" {
		t.Errorf("fields = %+v", l.Fields)
	}

	// Every part of the page has what InScope and the filters read.
	for _, l := range ls {
		for _, k := range []string{attrCategoryID, attrMakeID, attrModelID, attrQuality} {
			if _, ok := l.Str(k); !ok {
				t.Errorf("%s: no %s in %v", l.ExternalID, k, l.Attrs)
			}
		}
		if _, ok := l.Num(attrYear); !ok || l.PriceValue == nil {
			t.Errorf("%s: no year or price", l.ExternalID)
		}
	}
}

// A part still gets the facts searches scope it by when the API leaves
// some out: a year range reads as its first year, a model without its make
// gets the make from the codebook.
func TestListingFillsGaps(t *testing.T) {
	var n part
	if err := json.Unmarshal([]byte(`{"id": 7, "url": "https://rrr.lt/en/used-part/x", "category": {"id": 230, "name": "Tank"},
		"vehicle": {"description": "BMW X3", "model": {"id": 1945, "name": "X3 G01"},
		"attributes": [{"type": "YEARS", "value": "2019-2021"}, {"type": "FUEL_TYPE", "value": "diesel"}]}}`), &n); err != nil {
		t.Fatal(err)
	}
	l := n.listing("make=1", time.Now())
	if y, ok := l.Num(attrYear); !ok || y != 2019 {
		t.Errorf("year = %v, %v; want 2019", y, ok)
	}
	if id, _ := l.Str(attrMakeID); id != "1" || l.Attrs[attrMake] != "BMW" {
		t.Errorf("make = %v %v; want BMW (1) from the codebook", l.Attrs[attrMake], id)
	}
	l.Feed = "cat=1"
	if !NewClient(time.Second).InScope([]string{"make=1&year=2018-2020"}, l) {
		t.Error("the part is out of a BMW 2018-2020 search's scope")
	}
}

func TestFetchFeedErrors(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusTooManyRequests)
	_, err := NewClient(5*time.Second).WithBaseURL(srv.URL).FetchFeed(context.Background(), fixtureFeed, 1)
	var se *StatusError
	if !errors.As(err, &se) || se.HTTPStatus() != http.StatusTooManyRequests {
		t.Errorf("err = %v, want a 429 StatusError", err)
	}

	gqlErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"x\""}]}`))
	}))
	defer gqlErr.Close()
	if _, err := NewClient(5*time.Second).WithBaseURL(gqlErr.URL).FetchFeed(context.Background(), fixtureFeed, 1); err == nil || !strings.Contains(err.Error(), "Cannot query field") {
		t.Errorf("err = %v, want the GraphQL error", err)
	}

	if _, err := NewClient(time.Second).FetchFeed(context.Background(), "colour=red", 1); err == nil {
		t.Error("a malformed feed was fetched")
	}
}

func TestFeedIsCanonical(t *testing.T) {
	two, max300 := 2005, 300
	f := feedSpec{
		Cats: []int{230, 197, 230}, Makes: []int{5}, Models: [][2]int{{1, 23}, {1, 7}},
		Year: span{Min: &two}, Price: span{Max: &max300}, Side: []string{"RIGHT", "LEFT"},
	}
	want := "cat=197,230&make=5&model=1:7,1:23&year=2005-&price=-300&side=LEFT,RIGHT"
	if got := f.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	back, err := parseFeed(want)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.String(); got != want {
		t.Errorf("round trip = %q, want %q", got, want)
	}
	for _, bad := range []string{"cat", "cat=x", "model=1", "year=2005", "colour=red"} {
		if _, err := parseFeed(bad); err == nil {
			t.Errorf("parseFeed(%q) accepted", bad)
		}
	}
}

func TestInScope(t *testing.T) {
	l := fetchFixture(t)[0] // BMW X3 G01 2022, category 230, 94.12 €, automatic, used
	c := NewClient(time.Second)
	for feed, want := range map[string]bool{
		"cat=197":                   true, // an ancestor of 230
		"cat=230":                   true,
		"cat=205":                   false, // a sibling branch
		"make=1":                    true,
		"make=5":                    false,
		"model=1:1945":              true,
		"model=1:23":                false,
		"make=5&model=1:1945":       true, // either a whole make or the model
		"cat=197&make=1&year=2020-": true,
		"year=2023-":                false,
		"price=-50":                 false,
		"price=90-100":              true,

		"quality=NEW,USED":            true,
		"side=LEFT,LEFT_FRONT":        false, // the part has no side marked
		"cat=197&make=1&quality=USED": true,
	} {
		other := l
		other.Feed = "cat=1"
		if got := c.InScope([]string{feed}, other); got != want {
			t.Errorf("InScope(%q) = %v, want %v", feed, got, want)
		}
	}
	// From the search's own feed: the site filtered it already.
	if !c.InScope([]string{"side=LEFT"}, events.ListingDiscovered{Feed: "side=LEFT"}) {
		t.Error("a part from the search's own feed is out of scope")
	}
	left := l
	left.Feed, left.Attrs = "cat=1", maps.Clone(l.Attrs)
	left.Attrs[attrSide] = "LEFT_FRONT"
	for feed, want := range map[string]bool{"side=LEFT,LEFT_FRONT,LEFT_REAR": true, "side=RIGHT,RIGHT_FRONT,RIGHT_REAR": false} {
		if got := c.InScope([]string{feed}, left); got != want {
			t.Errorf("left front part: InScope(%q) = %v, want %v", feed, got, want)
		}
	}
	if got := c.PlanFeeds([]string{"make=1", "cat=197", "make=1"}, 1); !reflect.DeepEqual(got, []string{"cat=197", "make=1"}) {
		t.Errorf("PlanFeeds = %v", got)
	}
}

func TestCodebook(t *testing.T) {
	cb, err := PartsCodebook()
	if err != nil {
		t.Fatal(err)
	}
	if len(cb.Categories) < 1000 || len(cb.Makes) < 100 {
		t.Fatalf("codebook too small: %d categories, %d makes", len(cb.Categories), len(cb.Makes))
	}
	if got := cb.Ancestors(230); !reflect.DeepEqual(got, []int{230, 227, 197}) {
		t.Errorf("Ancestors(230) = %v", got)
	}
	if got := cb.Ancestors(999999999); !reflect.DeepEqual(got, []int{999999999}) {
		t.Errorf("Ancestors(unknown) = %v", got)
	}
	bmw, ok := cb.MakeByName("bmw")
	if !ok {
		t.Fatal("no BMW")
	}
	for _, name := range []string{"X3 G01", "x3 g01 (2018–2024)"} {
		if m, ok := bmw.Model(name); !ok || m.ID != 1945 {
			t.Errorf("Model(%q) = %v, %v", name, m, ok)
		}
	}
	// Paths are unique, so a form can offer them as plain options.
	seen := map[string]bool{}
	for _, p := range cb.paths {
		if seen[p] {
			t.Errorf("duplicate path %q", p)
		}
		seen[p] = true
	}
	// A form offers the 19 top groups before any subgroup or part.
	opts := cb.PartOptions()
	if len(opts) != len(cb.paths) || strings.Contains(opts[0], pathSep) || !strings.Contains(opts[50], pathSep) ||
		!slices.Contains(opts[:30], "Wheels/tires/caps") {
		t.Errorf("part options start %q", opts[:min(len(opts), 25)])
	}

	// The snapshot round-trips through its own format.
	raw, err := cb.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseCodebook(raw)
	if err != nil || len(again.Categories) != len(cb.Categories) || len(again.Makes) != len(cb.Makes) {
		t.Errorf("round trip: %v", err)
	}
}

func TestCompileSearch(t *testing.T) {
	c := NewClient(time.Second)
	got, err := c.CompileSearch(CategoryParts, map[string]any{
		"part":    []any{"air conditioning-heating system/radiators > radiators/fans", "Air conditioning-heating system/radiators › Radiators/fans › Coolant expansion tank/reservoir"},
		"make":    []any{"bmw", "Audi"},
		"model":   []any{"X3 G01"},
		"year":    map[string]any{"min": 2018.0, "max": nil},
		"price":   map[string]any{"max": 150.0},
		"quality": []any{"used", "NEW"},
		"side":    []any{"left", "Front"},
		"code":    []any{"7925685", " 7925685 "},
	})
	if err != nil {
		t.Fatal(err)
	}
	audi, _ := mustCodebook(t).MakeByName("Audi")
	wantFeed := feedSpec{
		Cats: []int{227}, Makes: []int{audi.ID}, Models: [][2]int{{1, 1945}},
		Year: span{Min: ptr(2018)}, Price: span{Max: ptr(150)},
		Quality: []string{"USED", "NEW"},
		Side:    []string{"LEFT", "LEFT_FRONT", "LEFT_REAR", "FRONT", "LEFT_FRONT", "RIGHT_FRONT"},
	}.String()
	if !reflect.DeepEqual(got.Feeds, []string{wantFeed}) {
		t.Errorf("feeds = %v, want %v", got.Feeds, wantFeed)
	}
	wantSpec := map[string]any{
		"part": []any{
			"Air conditioning-heating system/radiators › Radiators/fans",
			"Air conditioning-heating system/radiators › Radiators/fans › Coolant expansion tank/reservoir",
		},
		"make": []any{"BMW", "Audi"}, "model": []any{"X3 G01 (2018–2024)"},
		"year": map[string]any{"min": 2018.0}, "price": map[string]any{"max": 150.0},
		"quality": []any{"Used", "New"}, "side": []any{"Left", "Front"}, "code": []any{"7925685"},
	}
	if !reflect.DeepEqual(got.Spec, wantSpec) {
		t.Errorf("spec =\n%v\nwant\n%v", got.Spec, wantSpec)
	}
	wantFilters := map[string]any{TextAttr: map[string]any{"contains": []any{"7925685"}}}
	if !reflect.DeepEqual(got.Filters, wantFilters) {
		t.Errorf("filters = %v", got.Filters)
	}

	// The compiled feed is in scope for the fixture's first part, once it
	// has a side.
	l := fetchFixture(t)[0]
	l.Attrs[attrSide] = "LEFT_REAR"
	if !c.InScope(got.Feeds, l) {
		t.Error("the fixture part is out of the compiled search's scope")
	}
}

func TestCompileSearchMistakes(t *testing.T) {
	c := NewClient(time.Second)
	for name, tc := range map[string]struct {
		category string
		filters  map[string]any
		want     string
	}{
		"category":        {"cars", map[string]any{"make": "BMW"}, "unknown category"},
		"nothing chosen":  {CategoryParts, map[string]any{"price": map[string]any{"max": 10.0}}, "choose a part or a car make"},
		"unknown filter":  {CategoryParts, map[string]any{"make": "BMW", "colour": "red"}, `unknown filter "colour"`},
		"unknown part":    {CategoryParts, map[string]any{"part": "Turbbo"}, `did you mean "Engine › Turbocharger › Turbo"`},
		"ambiguous part":  {CategoryParts, map[string]any{"part": "Parcel shelf"}, "several groups"},
		"unknown make":    {CategoryParts, map[string]any{"make": "BMV"}, `did you mean "BMW"`},
		"model, no make":  {CategoryParts, map[string]any{"part": "Turbo", "model": "X3 G01"}, "make of the models first"},
		"model elsewhere": {CategoryParts, map[string]any{"make": "Audi", "model": "X3 G01"}, "unknown model"},
		"bad range":       {CategoryParts, map[string]any{"make": "BMW", "year": map[string]any{"min": 2020.0, "max": 2010.0}}, "above max"},
		"bad option":      {CategoryParts, map[string]any{"make": "BMW", "side": "Upside"}, "unknown value"},
		"dropped filter":  {CategoryParts, map[string]any{"make": "BMW", "fuel": "Diesel"}, `unknown filter "fuel"`},
	} {
		_, err := c.CompileSearch(tc.category, tc.filters)
		var inv *sources.InvalidSearch
		if !errors.As(err, &inv) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want an InvalidSearch with %q", name, err, tc.want)
		}
	}
}

func TestCategoriesDescribeTheForm(t *testing.T) {
	cats := NewClient(time.Second).Categories()
	if len(cats) != 1 || cats[0].ID != CategoryParts {
		t.Fatalf("categories = %+v", cats)
	}
	keys := map[string]sources.FieldSpec{}
	for _, f := range cats[0].Fields {
		keys[f.Key] = f
	}
	for _, k := range filterKeys {
		if _, ok := keys[k]; !ok {
			t.Errorf("no field %q", k)
		}
	}
	if m := keys["model"]; m.DependsOn != "make" || len(m.OptionsBy["BMW"]) == 0 {
		t.Errorf("model field = %+v", m.DependsOn)
	}
	// Every option the form offers compiles.
	for _, k := range []string{"part", "make", "quality", "side"} {
		for _, o := range keys[k].Options {
			f := map[string]any{k: o}
			if k != "make" {
				f["make"] = "BMW"
			}
			if _, err := NewClient(time.Second).CompileSearch(CategoryParts, f); err != nil {
				t.Fatalf("option %s=%q: %v", k, o, err)
			}
		}
	}
}

func mustCodebook(t *testing.T) *Codebook {
	t.Helper()
	cb, err := PartsCodebook()
	if err != nil {
		t.Fatal(err)
	}
	return cb
}

func ptr(n int) *int { return &n }
