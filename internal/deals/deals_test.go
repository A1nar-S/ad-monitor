package deals

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
)

func price(v float64) *float64 { return &v }

// english is a source that already writes this package's words.
func english(key, value string) string {
	if Knows(key, value) {
		return value
	}
	return ""
}

func TestCarOf(t *testing.T) {
	l := events.ListingDiscovered{PriceValue: price(8900), Attrs: map[string]any{
		"make": "BMW", "model": "520", "year": 2016.0, "engine_l": 2.0, "fuel": "diesel",
		"mileage_km": 189000.0, "gearbox": "automatic", "equipment": []any{"cruise control", "spoiler", "xenon lights"},
	}}
	c, ok := CarOf(l, english)
	want := Car{Make: "BMW", Model: "520", Fuel: Diesel, Gearbox: Automatic, Year: 2016, EngineL: 2.0, MileageKm: 189000, Price: 8900,
		Equipment: []string{"cruise control", "xenon lights"}}
	if !ok || !reflect.DeepEqual(c, want) {
		t.Fatalf("CarOf = %+v %v, want %+v", c, ok, want)
	}

	l.Attrs["fuel"], l.Attrs["gearbox"] = "hydrogen", "sequential"
	if c, _ := CarOf(l, english); c.Fuel != "" || c.Gearbox != "" {
		t.Errorf("untranslatable = %q, %q; want unknown", c.Fuel, c.Gearbox)
	}
	for _, drop := range []string{"make", "model", "year"} {
		l := events.ListingDiscovered{Attrs: map[string]any{"make": "BMW", "model": "520", "year": 2016.0}}
		delete(l.Attrs, drop)
		if _, ok := CarOf(l, english); ok {
			t.Errorf("without %s: got a car", drop)
		}
	}
}

func peersAt(prices ...float64) []Peer {
	ps := make([]Peer, len(prices))
	for i, p := range prices {
		ps[i] = Peer{Price: p, Year: 2016, MileageKm: 180000, Fuel: Diesel}
	}
	return ps
}

func TestRatePrice(t *testing.T) {
	car := Car{Year: 2016, MileageKm: 189000, Price: 8500}

	r, ok := RatePrice(car, peersAt(10000, 10200, 10000, 9800, 10500))
	if !ok {
		t.Fatal("no rating from 5 peers")
	}
	if r.Median != 10000 || r.Peers != 5 || r.Verdict != Steal {
		t.Errorf("rating = %+v, want median 10000 over 5, a steal", r)
	}
	if want := "🔥 Steal: 15% below the median of 5 similar ads (10 000 €; 2016, 180k km)"; r.Text() != want {
		t.Errorf("text = %q\nwant   %q", r.Text(), want)
	}

	if _, ok := RatePrice(car, peersAt(10000, 10000, 10000, 10000)); ok {
		t.Error("rated from 4 peers; too few to mean anything")
	}
	if _, ok := RatePrice(car, peersAt(10000, 10000, 10000, 10000, 1)); ok {
		t.Error("a 1 € placeholder counted as a peer")
	}
	if _, ok := RatePrice(Car{Year: 2016, Price: 1}, peersAt(1e4, 1e4, 1e4, 1e4, 1e4)); ok {
		t.Error("rated a 1 € placeholder price")
	}
	if _, ok := RatePrice(Car{Year: 2016, Price: 9000, Fuel: Petrol}, peersAt(1e4, 1e4, 1e4, 1e4, 1e4)); ok {
		t.Error("a petrol car priced against diesels")
	}
}

// The median is over the most similar ads, so cheap old wrecks don't make a
// newer car look overpriced.
func TestRatePriceNearest(t *testing.T) {
	var peers []Peer
	for range 15 {
		peers = append(peers, Peer{Price: 10000, Year: 2016, MileageKm: 150000})
	}
	for range 15 {
		peers = append(peers, Peer{Price: 3000, Year: 2014, MileageKm: 350000})
	}
	r, ok := RatePrice(Car{Year: 2016, MileageKm: 150000, Price: 10000}, peers)
	if !ok || r.Median != 10000 || r.Peers != 15 || r.YearMin != 2016 {
		t.Errorf("rating = %+v %v, want the 15 like it at 10 000", r, ok)
	}
}

func TestRatePriceWithoutMileage(t *testing.T) {
	r, ok := RatePrice(Car{Year: 2016, Price: 10000}, []Peer{
		{Price: 10000, Year: 2015}, {Price: 10000, Year: 2016}, {Price: 10000, Year: 2017},
		{Price: 10000, Year: 2016}, {Price: 10000, Year: 2016},
	})
	if !ok {
		t.Fatal("no rating")
	}
	if want := "😐 Fair: about the median of 5 similar ads (10 000 €; 2015–2017). Mileage unknown, so compared on year only."; r.Text() != want {
		t.Errorf("text = %q\nwant   %q", r.Text(), want)
	}
}

func TestVerdictFor(t *testing.T) {
	tests := []struct {
		diff float64
		want Verdict
	}{
		{-0.50, Suspicious}, {-0.40, Suspicious}, {-0.30, Steal}, {-0.15, Steal}, {-0.10, Good},
		{-0.05, Good}, {0, Fair}, {0.07, Fair}, {0.08, Pricey}, {0.19, Pricey}, {0.20, Overpriced},
	}
	for _, tt := range tests {
		if got := verdictFor(tt.diff); got != tt.want {
			t.Errorf("verdictFor(%v) = %v, want %v", tt.diff, got, tt.want)
		}
	}
}

func TestSuspiciousText(t *testing.T) {
	r, _ := RatePrice(Car{Year: 2016, MileageKm: 180000, Price: 4000}, peersAt(1e4, 1e4, 1e4, 1e4, 1e4))
	if r.Verdict != Suspicious || !strings.HasSuffix(r.Text(), "Check for damage, a scam or a price typo.") {
		t.Errorf("60%% below: %q", r.Text())
	}
}

func TestEuros(t *testing.T) {
	for v, want := range map[float64]string{350: "350 €", 1000: "1 000 €", 10350: "10 350 €", 125000.4: "125 000 €"} {
		if got := Euros(v); got != want {
			t.Errorf("Euros(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestCodebookLoads(t *testing.T) {
	cb, err := loadCodebook()
	if err != nil {
		t.Fatal(err)
	}
	if len(cb.Engines) == 0 || len(cb.Gearboxes) == 0 {
		t.Fatalf("%d engines, %d gearboxes", len(cb.Engines), len(cb.Gearboxes))
	}
	for _, s := range cb.Gearboxes {
		if s.Gearbox == "" {
			t.Errorf("gearbox entry %q names no gearbox", s.Name)
		}
	}
	if _, err := loadEquipmentBook(); err != nil {
		t.Fatal(err)
	}
}

func TestCodebookIsEnglish(t *testing.T) {
	for _, s := range []Spec{
		{Makes: []string{"BMW"}, Years: [2]int{2000, 2001}, Name: "x", Notes: "x", Rating: RatingOK, Fuel: "gasoline"},
		{Makes: []string{"BMW"}, Years: [2]int{2000, 2001}, Name: "x", Notes: "x", Rating: RatingOK, Gearbox: "tiptronic"},
	} {
		if s.validate() == nil {
			t.Errorf("%+v passed; the codebook takes this package's words only", s)
		}
	}
}

func TestRateEngine(t *testing.T) {
	tests := []struct {
		name string
		car  Car
		want string // Note.Name; "" = no entry
		rate Rating
	}{
		{"N47 years", Car{Make: "BMW", Model: "520", Fuel: Diesel, EngineL: 2.0, Year: 2011}, "2.0d N47", RatingRisky},
		{"N47 to B47 switch", Car{Make: "Bmw", Model: "320", Fuel: Diesel, EngineL: 2.0, Year: 2015}, "2.0d N47 or B47", RatingMixed},
		{"B47 years", Car{Make: "BMW", Model: "520", Fuel: Diesel, EngineL: 2.0, Year: 2017}, "2.0d B47", RatingGood},
		{"M54 before N52", Car{Make: "BMW", Model: "330", Fuel: Petrol, EngineL: 3.0, Year: 2004}, "2.2/2.5/3.0 M54 6-cylinder", RatingGood},
		{"N52 after M54", Car{Make: "BMW", Model: "330", Fuel: Petrol, EngineL: 3.0, Year: 2007}, "3.0 N52/N53/N54/N55", RatingMixed},
		{"model entry beats make entry", Car{Make: "Volkswagen", Model: "Touareg", Fuel: Diesel, EngineL: 2.5, Year: 2005}, "2.5 TDI 5-cylinder", RatingRisky},
		{"make entry", Car{Make: "Volkswagen", Model: "Passat (B5)", Fuel: Diesel, EngineL: 2.5, Year: 2003}, "2.5 TDI V6", RatingRisky},
		{"hybrid needs no size", Car{Make: "Toyota", Model: "Prius", Fuel: Hybrid, Year: 2012}, "Toyota/Lexus hybrid", RatingGood},
		{"electric of any make", Car{Make: "Tesla", Model: "Model 3", Fuel: Electric, Year: 2021}, "Electric", RatingOK},
		{"size rounding", Car{Make: "Ford", Model: "Fiesta", Fuel: Petrol, EngineL: 1.25, Year: 2008}, "1.25/1.4 Zetec/Duratec", RatingGood},
		{"Mazda's own 1.5 diesel", Car{Make: "Mazda", Model: "CX-3", Fuel: Diesel, EngineL: 1.5, Year: 2016}, "1.5 Skyactiv-D", RatingMixed},
		{"unknown make", Car{Make: "Lada", Model: "Niva", Fuel: Petrol, EngineL: 1.7, Year: 2010}, "", ""},
		{"unknown fuel", Car{Make: "BMW", Model: "520", EngineL: 2.0, Year: 2011}, "", ""},
		{"out of years", Car{Make: "BMW", Model: "520", Fuel: Diesel, EngineL: 2.0, Year: 1995}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := RateEngine(tt.car)
			if ok != (tt.want != "") || n.Name != tt.want || n.Rating != tt.rate {
				t.Errorf("RateEngine = %+v %v, want %q %q", n, ok, tt.want, tt.rate)
			}
		})
	}
}

func TestRateGearbox(t *testing.T) {
	golf := Car{Make: "Volkswagen", Model: "Golf 6", Fuel: Petrol, EngineL: 1.4, Year: 2011}
	if _, ok := RateGearbox(golf); ok {
		t.Error("rated an unknown gearbox; only the ad page tells it")
	}
	golf.Gearbox = Manual
	if _, ok := RateGearbox(golf); ok {
		t.Error("rated a manual")
	}
	golf.Gearbox = Automatic
	if n, ok := RateGearbox(golf); !ok || n.Rating != RatingRisky || !strings.Contains(n.Name, "DQ200") {
		t.Errorf("1.4 automatic Golf = %+v %v, want the DQ200", n, ok)
	}
}

// Two entries of equal specificity that both cover a car make the file's
// order decide silently; every car should have at most one answer.
func TestCodebookHasNoOverlaps(t *testing.T) {
	cb, err := loadCodebook()
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]Spec{cb.Engines, cb.Gearboxes} {
		for i, a := range list {
			for _, b := range list[i+1:] {
				if specificity(a) == specificity(b) && sameCars(a, b) {
					t.Errorf("%q %v and %q %v cover the same cars", a.Name, a.Years, b.Name, b.Years)
				}
			}
		}
	}
}

func sameCars(a, b Spec) bool {
	anyOf := func(x, y []string) bool {
		return len(x) == 0 || len(y) == 0 || overlaps(x, y, strings.EqualFold)
	}
	makes := slicesHave(a.Makes, "*") || slicesHave(b.Makes, "*") || overlaps(a.Makes, b.Makes, strings.EqualFold)
	return makes &&
		(a.Fuel == "" || b.Fuel == "" || a.Fuel == b.Fuel) &&
		a.Gearbox == b.Gearbox &&
		(len(a.Litres) == 0 || len(b.Litres) == 0 || overlaps(a.Litres, b.Litres, func(x, y float64) bool { return x == y })) &&
		a.Years[0] <= b.Years[1] && b.Years[0] <= a.Years[1] &&
		anyOf(a.Models, b.Models)
}

func overlaps[T any](a, b []T, eq func(T, T) bool) bool {
	for _, x := range a {
		for _, y := range b {
			if eq(x, y) {
				return true
			}
		}
	}
	return false
}

func slicesHave(a []string, v string) bool { return overlaps(a, []string{v}, strings.EqualFold) }

func TestEquipmentScore(t *testing.T) {
	// Climate control implies A/C: counted once, and A/C is not "missing".
	eq := equipmentOf([]string{"climate control", "air conditioning", "spoiler"})
	if eq.score != 2 || !eq.have["air conditioning"] {
		t.Errorf("climate + A/C: score %d, have %v; want 2 with A/C", eq.score, eq.have)
	}
	if got := notable(eq); !reflect.DeepEqual(got, []string{"climate control"}) {
		t.Errorf("notable = %q, want just climate control", got)
	}
}

func TestClassOf(t *testing.T) {
	book, err := loadEquipmentBook()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ make, model, want string }{
		{"Dacia", "Logan", "budget"},
		{"Volkswagen", "Up!", "budget"},
		{"Opel", "Astra", "mainstream"},
		{"BMW", "520", "premium"},
		{"BMW", "740", "luxury"},
		{"Mercedes", "S350", "luxury"},
		{"Mercedes", "CLA180", "premium"},
		{"Mercedes", "GLC 220", "premium"},
		{"Mercedes", "G350", "luxury"},
		{"Land Rover", "Range Rover Evoque", "premium"},
		{"Land Rover", "Range Rover Sport", "luxury"},
	} {
		if got := book.classOf(Car{Make: tt.make, Model: tt.model}); got != tt.want {
			t.Errorf("%s %s = %s, want %s", tt.make, tt.model, got, tt.want)
		}
	}
}

func TestRateEquipment(t *testing.T) {
	rich := []string{"climate control", "air conditioning", "adaptive cruise control", "reversing camera", "4x4", "xenon lights"}
	basic := []string{"air conditioning"}

	if _, ok := RateEquipment(Car{Year: 2016}, nil); ok {
		t.Error("rated a car whose equipment is unknown")
	}

	// Against similar ads with known equipment.
	var peers []Peer
	for range 6 {
		peers = append(peers, Peer{Equipment: basic})
	}
	peers = append(peers, Peer{Equipment: rich}, Peer{}) // a tie, and an ad never read
	r, ok := RateEquipment(Car{Make: "Skoda", Model: "Roomster", Year: 2016, Equipment: rich}, peers)
	if !ok || r.Level != WellEquipped || r.Better != 6 || r.Of != 7 {
		t.Fatalf("rating = %+v %v, want well equipped, better than 6 of 7", r, ok)
	}
	want := "👍 Well equipped: better than 6 of 7 similar ads. Has 4x4, adaptive cruise control, climate control, reversing camera, xenon lights. " +
		"No rear parking sensors, which mainstream cars of its age usually have."
	if r.Text() != want {
		t.Errorf("text = %q\nwant   %q", r.Text(), want)
	}
	var richPeers []Peer
	for range 7 {
		richPeers = append(richPeers, Peer{Equipment: rich})
	}
	if r, _ := RateEquipment(Car{Year: 2016, Equipment: basic}, richPeers); r.Level != Basic {
		t.Errorf("A/C only among well equipped ones: %v, want basic", r.Level)
	}

	// Too few to compare: judged by what its class has at its age. The
	// same kit is generous on a Dacia and thin on a BMW.
	kit := []string{"climate control", "cruise control", "rear parking sensors", "front parking sensors", "alloy wheels", "rain sensor"}
	if r, _ := RateEquipment(Car{Make: "Dacia", Model: "Logan", Year: 2012, Equipment: kit}, nil); r.Level != WellEquipped || r.Of != 0 || len(r.Missing) != 0 {
		t.Errorf("Dacia: %+v, want well equipped with nothing missing", r)
	}
	r, _ = RateEquipment(Car{Make: "BMW", Model: "X4", Year: 2017, Equipment: kit}, nil)
	want = "👎 Basic for a premium car of its age (too few similar ads with known equipment to compare). " +
		"Has climate control, alloy wheels, cruise control, front parking sensors, rain sensor, rear parking sensors. " +
		"No reversing camera, which premium cars of its age usually have."
	if r.Text() != want {
		t.Errorf("BMW text = %q\nwant       %q", r.Text(), want)
	}
}

func TestRateTrim(t *testing.T) {
	golf := func(eq ...string) Car { return Car{Make: "Volkswagen", Model: "Golf 7", Year: 2015, Equipment: eq} }

	r, ok := RateEquipment(golf("air conditioning"), nil)
	want := "👎 Base trim: has 0 of the 19 options a Volkswagen Golf 7 could have. Has air conditioning. " +
		"Lacks 4x4, adaptive cruise control, auxiliary heater, climate control."
	if !ok || r.Level != Basic || r.Text() != want {
		t.Errorf("base Golf = %v %v %q\nwant %q", r.Level, ok, r.Text(), want)
	}

	loaded := golf("climate control", "adaptive cruise control", "rear parking sensors", "front parking sensors", "reversing camera",
		"xenon lights", "alloy wheels", "rain sensor", "keyless entry", "panoramic roof", "lane assist", "blind-spot assist")
	r, _ = RateEquipment(loaded, nil)
	if r.Level != WellEquipped || r.OptionsHad != 13 || r.Options != 19 || len(r.Unticked) != 0 {
		t.Errorf("loaded Golf = %+v, want near top with 13 of 19 (cruise comes with adaptive cruise)", r)
	}

	// Trims win over similar ads: the codebook knows what the model offered.
	var peers []Peer
	for range 7 {
		peers = append(peers, Peer{Equipment: []string{"air conditioning"}})
	}
	if r, _ := RateEquipment(golf("air conditioning", "cruise control", "alloy wheels"), peers); r.Trim == "" || r.Level != Basic {
		t.Errorf("against peers: %+v, want judged by trim, base", r)
	}

	r, _ = RateEquipment(golf("rain sensor"), nil)
	if !strings.HasSuffix(r.Text(), "The seller did not tick air conditioning, which every one has, so the list may be incomplete.") {
		t.Errorf("unticked base = %q", r.Text())
	}

	for _, tt := range []struct {
		make, model string
		year        int
		want        string
	}{
		{"Lexus", "IS", 2010, "Lexus IS (2005–2012)"},
		{"Audi", "A4", 2006, "Audi A4 B6/B7"},
		{"Land Rover", "Discovery Sport", 2016, "Land Rover Discovery Sport"},
		{"Land Rover", "Discovery", 2016, "Land Rover Discovery 4"},
		{"Mercedes", "GLE 350", 2017, "Mercedes ML/GLE W166"},
	} {
		if tr, _ := mustBook(t).trimOf(Car{Make: tt.make, Model: tt.model, Year: tt.year}); tr.Name != tt.want {
			t.Errorf("%s %s %d = %q, want %q", tt.make, tt.model, tt.year, tr.Name, tt.want)
		}
	}
	if _, ok := mustBook(t).trimOf(Car{Make: "Volkswagen", Model: "Golf 7", Year: 2022}); ok {
		t.Error("a 2022 Golf 7 matched; out of the generation's years")
	}
}

func mustBook(t *testing.T) *equipmentBook {
	t.Helper()
	b, err := loadEquipmentBook()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// One car, one generation: overlapping entries would let the file's order
// decide silently.
func TestTrimsHaveNoOverlaps(t *testing.T) {
	ms := mustBook(t).Models
	for i, a := range ms {
		for _, b := range ms[i+1:] {
			if overlaps(a.Makes, b.Makes, strings.EqualFold) && overlaps(a.Models, b.Models, strings.EqualFold) &&
				a.Years[0] <= b.Years[1] && b.Years[0] <= a.Years[1] {
				t.Errorf("%q %v and %q %v cover the same cars", a.Name, a.Years, b.Name, b.Years)
			}
		}
	}
}

func TestOverall(t *testing.T) {
	risky := &Note{Name: "2.0d N47", Rating: RatingRisky}
	good := &Note{Name: "2.0d B47", Rating: RatingGood}
	price := func(v Verdict, diff float64) *PriceRating { return &PriceRating{Verdict: v, Diff: diff} }
	tests := []struct {
		name string
		r    Ratings
		want Buy
		why  string
	}{
		{"pricey with a risky engine", Ratings{Price: price(Pricey, 0.12), Engine: risky}, BadBuy, "12% above similar ads, risky engine (2.0d N47)"},
		{"a steal cancels a risky engine", Ratings{Price: price(Steal, -0.2), Engine: risky}, AverageBuy, "20% below similar ads, risky engine (2.0d N47)"},
		{"fair, durable, well equipped", Ratings{Price: price(Fair, 0), Engine: good, Gearbox: good, Equipment: &EquipmentRating{Level: WellEquipped}}, GoodBuy,
			"priced like similar ads, durable engine (2.0d B47), durable gearbox (2.0d B47), well equipped"},
		{"fair and durable is not yet good", Ratings{Price: price(Fair, 0), Engine: good, Gearbox: good}, AverageBuy,
			"priced like similar ads, durable engine (2.0d B47), durable gearbox (2.0d B47)"},
		{"no price: engine and equipment", Ratings{Engine: risky, Equipment: &EquipmentRating{Level: Basic}}, BadBuy, "risky engine (2.0d N47), basic equipment"},
		{"suspicious price overrides", Ratings{Price: price(Suspicious, -0.5), Engine: good}, CheckFirst, "50% below similar ads, which usually means damage, a scam or a typo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, why, ok := tt.r.Overall()
			if !ok || b != tt.want || strings.Join(why, ", ") != tt.why {
				t.Errorf("Overall = %v %q %v, want %v %q", b, why, ok, tt.want, tt.why)
			}
		})
	}
	if _, _, ok := (Ratings{Engine: good}).Overall(); ok {
		t.Error("an overall verdict from the engine alone")
	}
}

func TestFields(t *testing.T) {
	car := Car{Make: "BMW", Model: "520", Fuel: Diesel, EngineL: 2.0, Year: 2016, MileageKm: 180000, Price: 9500, Gearbox: Automatic}
	fs := Fields(car, peersAt(1e4, 1e4, 1e4, 1e4, 1e4))
	var names []string
	for _, f := range fs {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ", "); got != "⭐ Overall, 🏷️ Price verdict, 📏 Mileage verdict, 🔧 Engine verdict, 🕹️ Gearbox verdict" {
		t.Errorf("fields = %s", got)
	}
	want := "✅ Potentially a good buy: 5% below similar ads, durable engine (2.0d B47), durable gearbox (ZF 6- or 8-speed automatic). Still have it inspected before paying."
	if fs[0].Value != want {
		t.Errorf("overall = %q\nwant      %q", fs[0].Value, want)
	}
	if fs[2].Value != "😐 Usual mileage: 180k km at 11 years, 0.9× the usual 196k km for its age." {
		t.Errorf("mileage line = %q", fs[2].Value)
	}
	if fs[3].Value != "👍 Good: 2.0d B47. Much more reliable than the N47 it replaced. Usual diesel items: DPF, EGR cooler (there was a recall), dual-mass flywheel." {
		t.Errorf("engine line = %q", fs[2].Value)
	}
	if fs := Fields(Car{Make: "Lada", Model: "Niva", Year: 2010, Price: 2000}, nil); len(fs) != 0 {
		t.Errorf("nothing to say, got %v", fs)
	}
}

func TestScore(t *testing.T) {
	good := &Note{Name: "x", Rating: RatingGood}
	steal := Ratings{Price: &PriceRating{Verdict: Steal, Diff: -0.25}, Engine: good}
	fair := Ratings{Price: &PriceRating{Verdict: Fair, Diff: 0.02}, Engine: good}
	if steal.Score() <= fair.Score() {
		t.Errorf("steal %v ranks below fair %v", steal.Score(), fair.Score())
	}
	odd := Ratings{Price: &PriceRating{Verdict: Suspicious, Diff: -0.6}, Engine: good}
	if !math.IsInf(odd.Score(), -1) || !math.IsInf((Ratings{Engine: good}).Score(), -1) {
		t.Error("suspicious or barely known cars should rank last")
	}
}
