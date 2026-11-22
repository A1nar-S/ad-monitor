package textutil

import "testing"

func TestEuros(t *testing.T) {
	for v, want := range map[float64]string{350: "350 €", 1000: "1 000 €", 10350: "10 350 €", 125000.4: "125 000 €"} {
		if got := Euros(v); got != want {
			t.Errorf("Euros(%v) = %q, want %q", v, got, want)
		}
	}
}
