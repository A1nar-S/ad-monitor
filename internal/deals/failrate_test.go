package deals

import (
	"strings"
	"testing"
)

// testFailJSON: all diesels of 2008–2012 fail 40%, petrols 30%; diesel
// Golfs of 2009–2011 20%, the one petrol year 50%; diesel Golf Pluses of
// 2010 60%; too few diesel Golf Variants to say.
const testFailJSON = `{"source":"test","years":[2022,2024],
"base":{
 "diesel":{"from":2008,"n":[2000,2000,2000,2000,2000],"fails":[800,800,800,800,800]},
 "petrol":{"from":2008,"n":[2000,2000,2000,2000,2000],"fails":[600,600,600,600,600]}},
"models":[
 {"make":"Volkswagen","model":"Golf","fuels":{
  "diesel":{"from":2009,"n":[200,200,200],"fails":[40,40,40]},
  "petrol":{"from":2010,"n":[100],"fails":[50]}}},
 {"make":"Volkswagen","model":"Golf Plus","fuels":{
  "diesel":{"from":2010,"n":[600],"fails":[360]}}},
 {"make":"Volkswagen","model":"Golf Variant","fuels":{
  "diesel":{"from":2010,"n":[50],"fails":[50]}}}
]}`

func TestEmbeddedFailTableLoads(t *testing.T) {
	ft, err := newFailTable(inspectionsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.Models) < 100 || len(ft.Base) == 0 {
		t.Errorf("%d models, %d base fuels: did the generator run?", len(ft.Models), len(ft.Base))
	}
}

func TestCoversModel(t *testing.T) {
	tests := []struct {
		key, model string
		want       bool
	}{
		{"golf", "golf5", true},
		{"passat", "passatb6", true},
		{"x5", "x5", true},
		{"golfplus", "golf5", false},
		{"6", "626", false}, // a prefix may not cut a number
		{"3", "323", false},
		{"", "golf", false},
	}
	for _, tt := range tests {
		if got := CoversModel(tt.key, tt.model); got != tt.want {
			t.Errorf("CoversModel(%q, %q) = %v", tt.key, tt.model, got)
		}
	}
	for _, tt := range []struct{ brand, model, want string }{
		{"Mazda", "Mazda6", "6"},
		{"Toyota", "RAV 4", "rav4"},
		{"Land Rover", "Land Rover", "landrover"},
		{"Volkswagen", "Passat (B6)", "passatb6"},
		{"Audi", "80 1,8 S", "80.1.8s"},
	} {
		if got := ModelKey(tt.brand, tt.model); got != tt.want {
			t.Errorf("ModelKey(%q, %q) = %q, want %q", tt.brand, tt.model, got, tt.want)
		}
	}
}

func TestRateReliability(t *testing.T) {
	tests := []struct {
		name             string
		car              Car
		n                int
		rate, typical, x float64
		yearMin, yearMax int
	}{
		{"a generation under the family", Car{Make: "VOLKSWAGEN", Model: "Golf 5", Fuel: Diesel, Year: 2010}, 600, 0.2, 0.4, 1, 2009, 2011},
		{"the longest model wins", Car{Make: "Volkswagen", Model: "Golf Plus", Fuel: Diesel, Year: 2010}, 600, 0.6, 0.4, -1, 2010, 2010},
		// Too few petrols, so all fuels, each against its own base.
		{"too few of its fuel", Car{Make: "Volkswagen", Model: "Golf 5", Fuel: Petrol, Year: 2010}, 700, 170.0 / 700, 270.0 / 700, 1, 2009, 2011},
		{"unknown fuel", Car{Make: "Volkswagen", Model: "Golf 5", Year: 2010}, 700, 170.0 / 700, 270.0 / 700, 1, 2009, 2011},
		{"too rare a variant falls back to its family", Car{Make: "Volkswagen", Model: "Golf Variant", Fuel: Diesel, Year: 2010}, 600, 0.2, 0.4, 1, 2009, 2011},
		{"wider years when too few", Car{Make: "Volkswagen", Model: "Golf", Fuel: Diesel, Year: 2012}, 400, 0.2, 0.4, 1, 2010, 2011},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := RateReliability(tt.car)
			if !ok {
				t.Fatal("not rated")
			}
			if r.Inspections != tt.n || !near(r.FailRate, tt.rate) || !near(r.Typical, tt.typical) || !near(r.X, tt.x) ||
				r.YearMin != tt.yearMin || r.YearMax != tt.yearMax {
				t.Errorf("got %+v", r)
			}
		})
	}
	for _, c := range []Car{
		{Make: "Volkswagen", Model: "Golf 5", Fuel: Diesel, Year: 2016}, // no years near
		{Make: "Volkswagen", Model: "Polo", Fuel: Diesel, Year: 2010},   // not in the table
		{Make: "Opel", Model: "Golf", Fuel: Diesel, Year: 2010},         // another make
	} {
		if r, ok := RateReliability(c); ok {
			t.Errorf("%v rated %+v", c, r)
		}
	}
}

func TestReliabilityText(t *testing.T) {
	r, _ := RateReliability(Car{Make: "Volkswagen", Model: "Golf 5", Fuel: Diesel, Year: 2010})
	if got, want := r.Text(), "Rarely fails inspection: 20% of such cars (2009–2011) fail the technical inspection at the first attempt, 40% of all cars that age (CSDD 2022–2024, 600 inspections)."; got != want {
		t.Errorf("Text = %q\nwant   %q", got, want)
	}
	if got, want := r.TextIn(Lv), "Reti neiztur apskati: 20% šādu auto (2009–2011) neiztur tehnisko apskati ar pirmo reizi, no visiem šī vecuma auto — 40% (CSDD 2022–2024, 600 apskates)."; got != want {
		t.Errorf("TextIn(Lv) = %q\nwant        %q", got, want)
	}
	bad, _ := RateReliability(Car{Make: "Volkswagen", Model: "Golf Plus", Fuel: Diesel, Year: 2010})
	cs := Ratings{Reliability: &bad}.Components()
	if len(cs) != 1 || cs[0].Key != "reliability" || cs[0].Points != -weightReliable || cs[0].Why != "such cars often fail inspection" {
		t.Errorf("components = %+v", cs)
	}
	if !strings.Contains(bad.Text(), "(2010)") {
		t.Errorf("one model year: %q", bad.Text())
	}
	if ls := LinesIn(Ratings{Reliability: &bad}, En); len(ls) != 1 || ls[0].Tone != ToneBad || ls[0].Label != "Often fails inspection" {
		t.Errorf("lines = %+v", ls)
	}
}

func TestSeriesAdd(t *testing.T) {
	var s Series
	s.Add(2010, true)
	s.Add(2012, false)
	s.Add(2008, true)
	if s.From != 2008 || len(s.N) != 5 || s.Total() != 3 {
		t.Fatalf("got %+v", s)
	}
	for y, want := range map[int][2]int{2008: {1, 1}, 2010: {1, 1}, 2011: {0, 0}, 2012: {1, 0}, 2020: {0, 0}} {
		if n, f := s.at(y); n != want[0] || f != want[1] {
			t.Errorf("at(%d) = %d, %d", y, n, f)
		}
	}
}
