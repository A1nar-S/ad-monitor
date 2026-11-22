package deals

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var emoji = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]`)

func TestLines(t *testing.T) {
	cases := latvianCases(t)
	good := LinesIn(cases["good buy"], En)
	var keys []string
	for _, ln := range good {
		keys = append(keys, ln.Key)
	}
	if got := strings.Join(keys, ","); got != "price,mileage,inspection,engine,gearbox,equipment" {
		t.Fatalf("lines = %s", got)
	}
	want := []Line{
		{Key: "price", Tone: ToneGreat, Label: "Steal", Text: "2 000 € (20%) below the median of 12 similar ads (10 000 €; 2015–2018, 140–230k km)"},
		{Key: "mileage", Tone: ToneGood, Label: "Low mileage", Text: "60k km at 6 years, 0.6× the usual 108k km for its age."},
		{Key: "inspection", Tone: ToneGood, Label: "Valid until 02.2028 (15 months left)"},
	}
	for i, w := range want {
		if g := good[i]; g.Key != w.Key || g.Tone != w.Tone || g.Label != w.Label || g.Text != w.Text {
			t.Errorf("line %d = %+v\nwant     %+v", i, g, w)
		}
	}
	if e := good[3]; e.Tone != ToneGood || e.Label != "Good" || e.Subject != "1.9 TDI" || !strings.HasPrefix(e.Text, "One of the most durable") {
		t.Errorf("engine = %+v", e)
	}
	if e := good[5]; e.Label != "Well equipped" || e.Text != "Better than 9 of 12 similar ads." ||
		!slices.Equal(e.Has, []string{"climate control", "4x4", "reversing camera"}) {
		t.Errorf("equipment = %+v", e)
	}
	trim := LinesIn(cases["average"], Lv)
	if e := trim[len(trim)-1]; e.Tone != ToneNeutral || e.Label != "Vidēja komplektācija" ||
		e.Text != "Ir 6 no 17 papildu opcijām, kādas var būt Volkswagen Golf 7." ||
		!slices.Equal(e.Lacks, []string{"aizmugurējie parkošanās sensori", "panorāmas jumts"}) || !slices.Equal(e.Unticked, []string{"kondicionieris"}) {
		t.Errorf("trim in Latvian = %+v", e)
	}
	if p := LinesIn(cases["too cheap"], En)[0]; p.Tone != ToneWarn || p.Label != "Suspiciously cheap" {
		t.Errorf("too cheap = %+v", p)
	}
	if m := LinesIn(cases["bad buy"], En)[1]; m.Tone != ToneAwful {
		t.Errorf("very high mileage = %+v", m)
	}

	// No emoji in any part, in either language: the page draws its own.
	for name, r := range cases {
		for _, l := range Langs {
			for _, ln := range LinesIn(r, l) {
				for _, s := range slices.Concat([]string{ln.Label, ln.Subject, ln.Text}, ln.Has, ln.Lacks, ln.Unticked) {
					if emoji.MatchString(s) && !strings.Contains(s, "360°") {
						t.Errorf("%s (%s): emoji in %q", name, l, s)
					}
				}
			}
		}
	}
}
