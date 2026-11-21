package textutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestFold(t *testing.T) {
	if got := Fold("  Šķūnis  Ūdens "); got != "skunis  udens" {
		t.Errorf("Fold = %q: case and accents off, trimmed, inner spaces kept", got)
	}
	if got := Normalize("  Šķūnis  Ūdens "); got != "skunis udens" {
		t.Errorf("Normalize = %q: also single spaces", got)
	}
	if got := Fold("Łódka Kėdainiai Ąžuolas Müller"); got != "lodka kedainiai azuolas muller" {
		t.Errorf("Fold = %q: the neighbours' accents too", got)
	}
}

func TestClosest(t *testing.T) {
	names := []string{"Volkswagen", "Volvo", "Toyota", "Engine › Turbocharger"}
	if got := Closest("volksvagen", names); len(got) == 0 || got[0] != "Volkswagen" {
		t.Errorf("Closest = %q, want Volkswagen first", got)
	}
	if got := Closest("turbo", names); len(got) != 0 {
		t.Errorf("Closest(turbo) = %q, want nothing: too far by edits", got)
	}
	if got := ClosestContaining("turbo", names); !reflect.DeepEqual(got, []string{"Engine › Turbocharger"}) {
		t.Errorf("ClosestContaining(turbo) = %q", got)
	}
	if got, want := DidYouMean([]string{"A", "B"}), ` (did you mean "A" or "B"?)`; got != want {
		t.Errorf("DidYouMean = %q, want %q", got, want)
	}
	if DidYouMean(nil) != "" {
		t.Error("a suggestion from nothing")
	}
}

func TestKeywords(t *testing.T) {
	got, err := Keywords([]string{" sport  line ", "Sport Line", "", "ACC"})
	if err != nil || !reflect.DeepEqual(got, []string{"sport line", "ACC"}) {
		t.Errorf("Keywords = %q, %v", got, err)
	}
	for _, bad := range [][]string{{"x"}, {strings.Repeat("a", MaxKeywordLen+1)}, strings.Fields(strings.Repeat("ab cd ef gh ij kl mn op qr st uv wx ", 1))} {
		if _, err := Keywords(bad); err == nil {
			t.Errorf("Keywords(%q) passed", bad)
		}
	}
}

func TestLists(t *testing.T) {
	if got := Uniq([]string{"a", "b", "a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Uniq = %q", got)
	}
	if got := NonEmpty(" a ", "", "  ", "b"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("NonEmpty = %q", got)
	}
	for _, v := range []any{3.0, 3, int64(3), uint64(3)} {
		if n, ok := Number(v); !ok || n != 3 {
			t.Errorf("Number(%T) = %v, %v", v, n, ok)
		}
	}
	if _, ok := Number("3"); ok {
		t.Error("a string read as a number")
	}
}
