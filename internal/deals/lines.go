package deals

import (
	"cmp"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tone is how a verdict line reads, for a page to colour it.
type Tone string

const (
	ToneGreat   Tone = "great"
	ToneGood    Tone = "good"
	ToneNeutral Tone = "neutral"
	ToneMixed   Tone = "mixed" // depends on the version
	ToneBad     Tone = "bad"
	ToneAwful   Tone = "awful"
	ToneWarn    Tone = "warn" // too cheap to trust
)

// The tones of the labels, in the order of their phrases.
var (
	verdictTones = [...]Tone{ToneWarn, ToneGreat, ToneGood, ToneNeutral, ToneBad, ToneAwful} // by Verdict
	mileageTones = [...]Tone{ToneGood, ToneNeutral, ToneBad, ToneAwful}                      // by mileage level
	levelTones   = [...]Tone{ToneBad, ToneNeutral, ToneGood}                                 // by Level
	ratingTones  = map[Rating]Tone{RatingGood: ToneGood, RatingOK: ToneNeutral, RatingMixed: ToneMixed, RatingRisky: ToneBad}
)

// Line is one verdict, in parts, for a page to lay out: what the alerts
// write as one "Good: 2.0d B47. Much more reliable…" line, with a tone for
// the page to colour it by.
type Line struct {
	Key     string   `json:"key"` // price, mileage, inspection, engine, gearbox, equipment
	Tone    Tone     `json:"tone"`
	Label   string   `json:"label"`             // "Steal", "Low mileage", "Valid until 10.2028 (23 months left)"
	Subject string   `json:"subject,omitempty"` // what is judged: the engine, the gearbox, the trim
	Text    string   `json:"text,omitempty"`    // the rest, as sentences
	Has     []string `json:"has,omitempty"`     // equipment it has, named in the line's language
	Lacks   []string `json:"lacks,omitempty"`   // equipment cars like it (or its better trims) have and it lacks
	// Unticked: equipment every one has that the seller did not tick, so
	// the list may be incomplete.
	Unticked []string `json:"unticked,omitempty"`
}

// split cuts an alert line into its label and the rest, at the first ": ".
// A line without one is all label.
func split(key string, tone Tone, s string) Line {
	label, rest, ok := strings.Cut(s, ": ")
	if !ok {
		label = strings.TrimSuffix(s, ".")
	}
	return Line{Key: key, Tone: tone, Label: label, Text: capitalise(rest)}
}

func capitalise(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// LinesIn are the verdict lines of r in l, in the order of FieldsIn after
// its overall line.
func LinesIn(r Ratings, l Lang) []Line {
	var out []Line
	if r.Price != nil {
		out = append(out, split("price", verdictTones[r.Price.Verdict], r.Price.TextIn(l)))
	}
	if r.Mileage != nil {
		out = append(out, split("mileage", mileageTones[r.Mileage.level()], r.Mileage.TextIn(l)))
	}
	if r.Inspection != nil {
		out = append(out, split("inspection", r.Inspection.tone(), r.Inspection.TextIn(l)))
	}
	if r.Engine != nil {
		out = append(out, r.Engine.line("engine", l))
	}
	if r.Gearbox != nil {
		out = append(out, r.Gearbox.line("gearbox", l))
	}
	if r.Equipment != nil {
		out = append(out, r.Equipment.line(l))
	}
	return out
}

func (n Note) line(key string, l Lang) Line {
	label := l.ph().rating[n.Rating]
	notes := n.Notes
	if l == Lv {
		notes = cmp.Or(n.NotesLv, n.Notes)
	}
	return Line{Key: key, Tone: ratingTones[n.Rating], Label: label, Subject: n.NameIn(l), Text: notes}
}

func (r EquipmentRating) line(l Lang) Line {
	p := l.ph()
	ln := Line{Key: "equipment", Has: l.names(r.Has), Lacks: l.names(r.Missing), Unticked: l.names(r.Unticked)}
	label := p.level[r.Level]
	switch {
	case r.Trim != "":
		label = p.trim[r.Level]
		ln.Text = capitalise(strings.TrimPrefix(fmtTrim(p, r), ": "))
	case r.Of > 0:
		ln.Text = capitalise(strings.TrimPrefix(fmtBetter(p, r), ": ")) + "."
	default:
		ln.Text = fmt.Sprintf(p.lineClass, l.class(r.Class))
	}
	ln.Tone, ln.Label = levelTones[r.Level], label
	return ln
}

// names are equipment names in l.
func (l Lang) names(features []string) []string {
	if len(features) == 0 {
		return nil
	}
	return strings.Split(l.features(features, "\x00"), "\x00")
}
