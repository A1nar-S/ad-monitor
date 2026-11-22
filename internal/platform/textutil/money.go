package textutil

import (
	"math"
	"strconv"
	"strings"
)

// Euros writes a price as the region does: "10 350 €".
func Euros(v float64) string { return Grouped(int(math.Round(v))) + " €" }

// Grouped writes n with its thousands apart: "10 350".
func Grouped(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(d)
	}
	return b.String()
}
