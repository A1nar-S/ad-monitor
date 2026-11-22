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

	// Judged by class: the text says against what.
	if bad := LinesIn(cases["bad buy"], En); bad[len(bad)-1].Key != "equipment" {
		t.Errorf("bad buy lines end with %s", bad[len(bad)-1].Key)
	} else if e := bad[len(bad)-1]; e.Key != "equipment" || e.Tone != ToneBad || e.Label != "Basic" ||
		e.Text != "Compared with what premium cars of its age usually have: too few similar ads with known equipment to compare." {
		t.Errorf("equipment by class = %+v", e)
	}
	if e := LinesIn(cases["pricey"], Lv)[1]; e.Label != "Labi aprīkots" || e.Tone != ToneGood || !strings.Contains(e.Text, "budžeta auto šajā vecumā") {
		t.Errorf("equipment by class in Latvian = %+v", e)
	}
	// Inspection lines without a ": " are all label, in Latvian too.
	if i := LinesIn(cases["good buy"], Lv)[2]; i.Label != "Derīga līdz 02.2028 (vēl 15 mēn.)" || i.Text != "" || i.Tone != ToneGood {
		t.Errorf("inspection in Latvian = %+v", i)
	}
	if i := LinesIn(cases["average"], En)[2]; i.Tone != ToneNeutral || !strings.HasPrefix(i.Label, "Valid until 01.2027, but runs out soon") ||
		i.Text != "Budget for the next one." {
		t.Errorf("inspection running out = %+v", i)
	}

	// The lines split from an alert line put it back together: label, ": ",
	// the text uncapitalised (or the label and "." with no text).
	for name, r := range cases {
		for _, l := range Langs {
			fields := FieldsIn(r, l)[1:]
			for i, ln := range LinesIn(r, l) {
				if ln.Key != "price" && ln.Key != "mileage" && ln.Key != "inspection" {
					continue
				}
				got := ln.Label + "."
				if ln.Text != "" {
					got = ln.Label + ": " + strings.ToLower(ln.Text[:1]) + ln.Text[1:]
				}
				if want := fields[i].Value; !strings.EqualFold(got, want) {
					t.Errorf("%s (%s) %s: %q from the lines, %q in the alert", name, l, ln.Key, got, want)
				}
			}
		}
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
