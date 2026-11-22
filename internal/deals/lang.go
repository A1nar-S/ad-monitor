package deals

import (
	"strconv"
	"strings"
)

// Lang is a language the verdicts can be written in. English is the
// package's own and the alerts'; the gem finder shows its readers theirs.
type Lang string

const (
	En Lang = "en"
	Lv Lang = "lv"
)

// Langs are the languages there are, English first.
var Langs = []Lang{En, Lv}

// phrases are every word a verdict uses, in one language. A format string
// takes its arguments in the order its English comment shows; Latvian
// reorders them with %[n] where it must.
type phrases struct {
	verdict  [6]string // by Verdict
	buy      [4]string // by Buy
	level    [3]string // by Level
	trim     [3]string // by Level
	rating   map[Rating]string
	mileage  [4]string // low, usual, high, very high
	class    map[string]string
	fields   [7]string // overall, price, mileage, inspection, engine, gearbox, equipment
	thousand string    // "k km"

	// Price: label, position, median, scope.
	priceLine       string // "%s: %s (%s; %s)"
	atMedian        string // "about the median of %d similar ads"
	belowMedian     string // "%s below the median of %d similar ads"
	aboveMedian     string // "%s above the median of %d similar ads"
	priceSuspicious string
	priceYearOnly   string

	// Mileage: label, km, age, ratio, expected km.
	mileageLine string
	years       func(n int) string // "6 years"

	inspectionGone   string // "%s" ran-out month
	inspectionNone   string
	inspectionValid  string
	inspectionSoon   string // until, months left
	inspectionUntil  string // until, months left
	monthsLeft       func(n int) string
	equipBetter      string // better, of
	equipClass       string // class
	lineClass        string // class: the equipment line's text when judged by class
	equipHas         string // list
	equipMissing     string // list, class
	equipMissingJoin string // between missing features
	trimLine         string // had, options, trim (after the label)
	trimLacks        string // list
	trimUnticked     string // list

	// Why, for the overall line and the breakdown.
	whyFairPrice      string
	whySuspicious     string // gap
	whyBelow          string // gap
	whyAbove          string // gap
	whyLowKm          string
	whyVeryHighKm     string
	whyHighKm         string
	whyNoInspection   string
	whyInspectionSoon string
	whyDurable        [2]string // engine, gearbox: name
	whyRisky          [2]string // engine, gearbox: name
	whyDepends        [2]string // engine, gearbox
	whyWellEquipped   string
	whyBasic          string

	overallGood  string // after a good buy
	overallCheck string // after one too cheap to trust
}

var enPhrases = &phrases{
	verdict: [...]string{"Suspiciously cheap", "Steal", "Good price", "Fair", "Pricey", "Overpriced"},
	buy:     [...]string{"Likely a bad buy", "Average buy", "Potentially a good buy", "Too cheap to trust"},
	level:   [...]string{"Basic", "Average", "Well equipped"},
	trim:    [...]string{"Base trim", "Mid trim", "Near top trim"},
	rating: map[Rating]string{
		RatingGood: "Good", RatingOK: "OK", RatingMixed: "Depends", RatingRisky: "Risky",
	},
	mileage:  [...]string{"Low mileage", "Usual mileage", "High mileage", "Very high mileage"},
	class:    map[string]string{},
	fields:   [...]string{"Overall", "Price verdict", "Mileage verdict", "Inspection verdict", "Engine verdict", "Gearbox verdict", "Equipment verdict"},
	thousand: "k km",

	priceLine:       "%s: %s (%s; %s)",
	atMedian:        "about the median of %d similar ads",
	belowMedian:     "%s below the median of %d similar ads",
	aboveMedian:     "%s above the median of %d similar ads",
	priceSuspicious: ". Check for damage, a scam or a price typo.",
	priceYearOnly:   ". Mileage unknown, so compared on year only.",

	mileageLine: "%s: %s at %s, %s× the usual %s for its age.",
	years:       func(n int) string { return strconv.Itoa(n) + " years" },

	inspectionGone:   "Ran out in %s: it may not be driven until it passes again, which may need repairs.",
	inspectionNone:   "No valid technical inspection: it may not be driven until it passes, which may need repairs.",
	inspectionValid:  "Valid technical inspection.",
	inspectionSoon:   "Valid until %s, but runs out soon (%s): budget for the next one.",
	inspectionUntil:  "Valid until %s (%s).",
	monthsLeft:       func(n int) string { return strconv.Itoa(n) + " months left" },
	equipBetter:      ": better than %d of %d similar ads",
	equipClass:       " for a %s car of its age (too few similar ads with known equipment to compare)",
	lineClass:        "Compared with what %s cars of its age usually have: too few similar ads with known equipment to compare.",
	equipHas:         " Has %s.",
	equipMissing:     " No %s, which %s cars of its age usually have.",
	equipMissingJoin: ", no ",
	trimLine:         ": has %d of the %d options a %s could have.",
	trimLacks:        " Lacks %s.",
	trimUnticked:     " The seller did not tick %s, which every one has, so the list may be incomplete.",

	whyFairPrice:      "priced like similar ads",
	whySuspicious:     "%s below similar ads, which usually means damage, a scam or a typo",
	whyBelow:          "%s below similar ads",
	whyAbove:          "%s above similar ads",
	whyLowKm:          "low mileage for its age",
	whyVeryHighKm:     "very high mileage for its age",
	whyHighKm:         "high mileage for its age",
	whyNoInspection:   "no valid technical inspection",
	whyInspectionSoon: "technical inspection runs out soon",
	whyDurable:        [...]string{"durable engine (%s)", "durable gearbox (%s)"},
	whyRisky:          [...]string{"risky engine (%s)", "risky gearbox (%s)"},
	whyDepends:        [...]string{"engine depends on the version", "gearbox depends on the version"},
	whyWellEquipped:   "well equipped",
	whyBasic:          "basic equipment",

	overallGood:  " Still have it inspected before paying.",
	overallCheck: " Inspect it before anything else.",
}

var lvPhrases = &phrases{
	verdict: [...]string{"Aizdomīgi lēts", "Ļoti izdevīga cena", "Laba cena", "Atbilstoša cena", "Dārgs", "Pārāk dārgs"},
	buy:     [...]string{"Visticamāk slikts piedāvājums", "Vidējs piedāvājums", "Potenciāli labs piedāvājums", "Pārāk lēts, lai uzticētos"},
	level:   [...]string{"Pamata aprīkojums", "Vidējs aprīkojums", "Labi aprīkots"},
	trim:    [...]string{"Bāzes komplektācija", "Vidēja komplektācija", "Gandrīz pilna komplektācija"},
	rating: map[Rating]string{
		RatingGood: "Labs", RatingOK: "Normāls", RatingMixed: "Atkarīgs no versijas", RatingRisky: "Riskants",
	},
	mileage: [...]string{"Mazs nobraukums", "Parasts nobraukums", "Liels nobraukums", "Ļoti liels nobraukums"},
	class: map[string]string{
		"budget": "budžeta", "mainstream": "vidējās klases", "premium": "premium klases", "luxury": "luksusa klases",
	},
	fields:   [...]string{"Kopvērtējums", "Cena", "Nobraukums", "Tehniskā apskate", "Dzinējs", "Ātrumkārba", "Aprīkojums"},
	thousand: " tūkst. km",

	priceLine:       "%s: %s (%s; %s)",
	atMedian:        "tuvu %d līdzīgu sludinājumu mediānai",
	belowMedian:     "%s zem %d līdzīgu sludinājumu mediānas",
	aboveMedian:     "%s virs %d līdzīgu sludinājumu mediānas",
	priceSuspicious: ". Pārbaudiet, vai nav bojājumu, krāpšanas vai kļūdas cenā.",
	priceYearOnly:   ". Nobraukums nav zināms, tāpēc salīdzināts tikai pēc gada.",

	mileageLine: "%s: %s %s vecumā, %s× no šim vecumam parastajiem %s.",
	years:       func(n int) string { return strconv.Itoa(n) + " " + lvPlural(n, "gada", "gadu") },

	inspectionGone:   "Beidzās %s: ar to nedrīkst braukt, kamēr tā nav izieta no jauna, un tam var būt vajadzīgs remonts.",
	inspectionNone:   "Nav derīgas tehniskās apskates: ar to nedrīkst braukt, kamēr tā nav izieta, un tam var būt vajadzīgs remonts.",
	inspectionValid:  "Derīga tehniskā apskate.",
	inspectionSoon:   "Derīga līdz %s, bet drīz beigsies (%s): rēķinieties ar nākamās apskates izmaksām.",
	inspectionUntil:  "Derīga līdz %s (%s).",
	monthsLeft:       func(n int) string { return "vēl " + strconv.Itoa(n) + " mēn." },
	equipBetter:      ": labāk nekā %d no %d līdzīgiem sludinājumiem",
	equipClass:       " %s auto savam vecumam (par maz līdzīgu sludinājumu ar zināmu aprīkojumu, lai salīdzinātu)",
	lineClass:        "Salīdzināts ar to, kas parasti ir %s auto šajā vecumā: par maz līdzīgu sludinājumu ar zināmu aprīkojumu, lai salīdzinātu.",
	equipHas:         " Ir: %s.",
	equipMissing:     " Nav: %s, lai gan %s auto šajā vecumā tas parasti ir.",
	equipMissingJoin: ", ",
	trimLine:         ": ir %d no %d papildu opcijām, kādas var būt %s.",
	trimLacks:        " Nav: %s.",
	trimUnticked:     " Pārdevējs nav atzīmējis: %s, lai gan tas ir katram, tāpēc saraksts var būt nepilnīgs.",

	whyFairPrice:      "cena kā līdzīgiem sludinājumiem",
	whySuspicious:     "%s lētāk par līdzīgiem; parasti tas nozīmē bojājumus, krāpšanu vai kļūdu",
	whyBelow:          "%s lētāk par līdzīgiem",
	whyAbove:          "%s dārgāk par līdzīgiem",
	whyLowKm:          "mazs nobraukums savam vecumam",
	whyVeryHighKm:     "ļoti liels nobraukums savam vecumam",
	whyHighKm:         "liels nobraukums savam vecumam",
	whyNoInspection:   "nav derīgas tehniskās apskates",
	whyInspectionSoon: "tehniskā apskate drīz beigsies",
	whyDurable:        [...]string{"izturīgs dzinējs (%s)", "izturīga ātrumkārba (%s)"},
	whyRisky:          [...]string{"riskants dzinējs (%s)", "riskanta ātrumkārba (%s)"},
	whyDepends:        [...]string{"dzinējs atkarīgs no versijas", "ātrumkārba atkarīga no versijas"},
	whyWellEquipped:   "labi aprīkots",
	whyBasic:          "pamata aprīkojums",

	overallGood:  " Pirms maksājat, tomēr ļaujiet to pārbaudīt meistaram.",
	overallCheck: " Vispirms to rūpīgi pārbaudiet.",
}

// ph is l's phrases; an unknown language gets English.
func (l Lang) ph() *phrases {
	if l == Lv {
		return lvPhrases
	}
	return enPhrases
}

// decimal writes x with one decimal, as l does: 1.9 or 1,9.
func (l Lang) decimal(x float64) string {
	s := strconv.FormatFloat(x, 'f', 1, 64)
	if l == Lv {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// km writes a distance in thousands: "210k km", "210 tūkst. km".
func (l Lang) km(v float64) string { return thousands(v) + l.ph().thousand }

// kmRange writes "140–230k km", or one figure when both round the same.
func (l Lang) kmRange(lo, hi float64) string {
	s := thousands(lo)
	if h := thousands(hi); h != s {
		s += "–" + h
	}
	return s + l.ph().thousand
}

// lvPlural picks the Latvian form for n: one for 1, 21, 31… (not 11).
func lvPlural(n int, one, many string) string {
	if n%10 == 1 && n%100 != 11 {
		return one
	}
	return many
}
