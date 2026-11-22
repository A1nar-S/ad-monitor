package deals

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// english words that must not show through a Latvian line.
var englishWord = regexp.MustCompile(`(?i)\b(the|of|ads?|below|above|similar|median|mileage|engine|gearbox|inspection|valid|until|years?|months?|has|lacks|equipped|equipment|durable|risky|buy|price|priced|usual|trim|options|seller|which|cars?|left|soon|depends|version)\b`)

// latvianCases cover every line and reason the verdicts can give.
func latvianCases(t *testing.T) map[string]Ratings {
	t.Helper()
	at := func(median, p float64) *PriceRating {
		r := priceVs(median, p)
		r.Peers, r.YearMin, r.YearMax, r.KmMin, r.KmMax, r.Detailed = 12, 2015, 2018, 140000, 230000, true
		return &r
	}
	cb, err := loadCodebook()
	if err != nil {
		t.Fatal(err)
	}
	note := func(s Spec) *Note {
		return &Note{Name: s.Name, Rating: s.Rating, Notes: s.Notes, NameLv: s.NameLv, NotesLv: s.NotesLv}
	}
	byRating := map[Rating]*Note{}
	for _, s := range cb.Engines {
		if byRating[s.Rating] == nil {
			byRating[s.Rating] = note(s)
		}
	}
	gearbox := note(cb.Gearboxes[len(cb.Gearboxes)-3]) // "ZF 6- or 8-speed automatic", named in Latvian
	month := func(s string) time.Time { m, _ := time.Parse("2006-01", s); return m }
	withEquipment := []string{"climate control", "4x4", "reversing camera"}
	missing := []string{"rear parking sensors", "panoramic roof"}

	cases := map[string]Ratings{
		"good buy": {Price: at(10000, 8000), Mileage: &MileageRating{Km: 60000, Age: 6, Expected: 108000, Ratio: 0.56, X: 0.8},
			Inspection: &InspectionRating{Valid: true, Until: month("2028-02"), MonthsLeft: 15},
			Engine:     byRating[RatingGood], Gearbox: gearbox,
			Equipment: &EquipmentRating{Level: WellEquipped, Better: 9, Of: 12, Has: withEquipment}},
		"bad buy": {Price: at(10000, 13000), Mileage: &MileageRating{Km: 310000, Age: 6, Expected: 108000, Ratio: 2.9, X: -1.25},
			Inspection: &InspectionRating{Until: month("2026-03")}, Engine: byRating[RatingRisky],
			Equipment: &EquipmentRating{Level: Basic, Class: "premium", Has: withEquipment[:1], Missing: missing}},
		"average": {Price: at(1500, 1800), Mileage: &MileageRating{Km: 250000, Age: 21, Expected: 378000, Ratio: 0.66, X: 0.1},
			Inspection: &InspectionRating{Valid: true, Until: month("2027-01"), MonthsLeft: 1}, Engine: byRating[RatingMixed],
			Equipment: &EquipmentRating{Level: Average, Trim: "Volkswagen Golf 7", OptionsHad: 6, Options: 17,
				Has: withEquipment, Missing: missing, Unticked: []string{"air conditioning"}}},
		"no inspection, high km": {Price: at(10000, 9000), Mileage: &MileageRating{Km: 200000, Age: 6, Expected: 108000, Ratio: 1.85, X: -0.5},
			Inspection: &InspectionRating{}, Engine: byRating[RatingOK]},
		"too cheap": {Price: at(15000, 7000), Engine: byRating[RatingGood]},
		"at the median, year only": {Price: func() *PriceRating {
			r := at(10000, 10050)
			r.Detailed, r.KmMax = false, 0
			return r
		}(), Inspection: &InspectionRating{Valid: true}, Equipment: &EquipmentRating{Level: Average, Trim: "Volkswagen Golf 7", Options: 17, OptionsHad: 14}},
		"pricey": {Price: at(10000, 11200), Equipment: &EquipmentRating{Level: WellEquipped, Class: "budget"}},
	}
	return cases
}

func TestLatvian(t *testing.T) {
	for name, r := range latvianCases(t) {
		en, lv := FieldsIn(r, En), FieldsIn(r, Lv)
		if len(lv) != len(en) || len(lv) < 3 {
			t.Fatalf("%s: %d Latvian lines for %d English", name, len(lv), len(en))
		}
		for i, f := range lv {
			if f.Value == "" || f.Value == en[i].Value || f.Name == en[i].Name {
				t.Errorf("%s: line %d not translated: %q %q", name, i, f.Name, f.Value)
			}
			// The engine and gearbox notes keep their codes ("2.0 TDI"),
			// which no English word matches.
			if w := englishWord.FindString(f.Value); w != "" {
				t.Errorf("%s: %q in %s %q", name, w, f.Name, f.Value)
			}
		}
		for _, c := range r.ComponentsIn(Lv) {
			if w := englishWord.FindString(c.Why); w != "" {
				t.Errorf("%s: %q in the %s reason %q", name, w, c.Key, c.Why)
			}
		}
	}
}

func TestLatvianLines(t *testing.T) {
	cases := latvianCases(t)
	good := FieldsIn(cases["good buy"], Lv)
	for i, want := range []string{
		"Potenciāli labs piedāvājums: 2 000 € (20%) lētāk par līdzīgiem, mazs nobraukums savam vecumam, izturīgs dzinējs (1.9 TDI), izturīga ātrumkārba (ZF 6 vai 8 pakāpju automāts), labi aprīkots. Pirms maksājat, tomēr ļaujiet to pārbaudīt meistaram.",
		"Ļoti izdevīga cena: 2 000 € (20%) zem 12 līdzīgu sludinājumu mediānas (10 000 €; 2015–2018, 140–230 tūkst. km)",
		"Mazs nobraukums: 60 tūkst. km 6 gadu vecumā, 0,6× no šim vecumam parastajiem 108 tūkst. km.",
		"Derīga līdz 02.2028 (vēl 15 mēn.).",
	} {
		if good[i].Value != want {
			t.Errorf("line %d = %q\nwant     %q", i, good[i].Value, want)
		}
	}
	if got := FieldsIn(cases["average"], Lv)[2].Value; !strings.Contains(got, "21 gada vecumā") {
		t.Errorf("21 years in Latvian takes the singular: %q", got)
	}
}

func TestFeaturesInLatvian(t *testing.T) {
	for n, f := range features {
		if f.lv == "" || f.lv == n && n != "4x4" {
			t.Errorf("feature %q has no Latvian name", n)
		}
	}
	for class := range mustEquipmentBook(t).Classes {
		if lvPhrases.class[class] == "" {
			t.Errorf("class %q has no Latvian name", class)
		}
	}
}

func mustEquipmentBook(t *testing.T) *equipmentBook {
	t.Helper()
	b, err := loadEquipmentBook()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLvPlural(t *testing.T) {
	for n, want := range map[int]string{1: "gada", 2: "gadu", 11: "gadu", 21: "gada", 111: "gadu", 0: "gadu"} {
		if got := lvPlural(n, "gada", "gadu"); got != want {
			t.Errorf("lvPlural(%d) = %s, want %s", n, got, want)
		}
	}
}
