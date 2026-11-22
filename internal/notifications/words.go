package notifications

import (
	"fmt"
	"strings"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
)

// Words are the notifier's own words in one alert language; a listing's
// facts come named by its source (events.Field.NameIn).
type Words struct {
	NewListing, Reposted string // headings
	Price, SeenBefore    string // field names
	ViewOn               string // source: "📎 View on %s"

	digestHead string // count, "matches", search, burst, window
	match      [2]string
	andMore    string // count
	minutes    string // window in minutes: "%d min"

	firstListed             string // date
	inOneEarlier, inEarlier string // count
	lastAskedNow            string // was, now, diff, more/less
	more, less, samePrice   string
	lastAsked               string // was
	dateLayout              string
	pluralOne               func(n int) bool
}

var wordsEn = &Words{
	NewListing: "🆕 New listing", Reposted: "♻️ Reposted listing",
	Price: "💰 Price", SeenBefore: "♻️ Seen before", ViewOn: "📎 View on %s",
	digestHead: "📦 **%d more %s for “%s”** (over %d in %s, so bundled)",
	match:      [2]string{"match", "matches"},
	andMore:    "…and %d more",
	minutes:    "%d min",

	firstListed:  "First listed %s",
	inOneEarlier: ", in 1 earlier ad.", inEarlier: ", in %d earlier ads.",
	lastAskedNow: " Last asked %s, now %s (%s %s).",
	more:         "more", less: "less", samePrice: " Same price as before.",
	lastAsked:  " Last asked %s.",
	dateLayout: "2 Jan 2006",
	pluralOne:  func(n int) bool { return n == 1 },
}

var wordsLv = &Words{
	NewListing: "🆕 Jauns sludinājums", Reposted: "♻️ Atkārtots sludinājums",
	Price: "💰 Cena", SeenBefore: "♻️ Redzēts agrāk", ViewOn: "📎 Skatīt vietnē %s",
	digestHead: "📦 **Vēl %d %s meklējumam “%s”** (vairāk nekā %d paziņojumi %s laikā, tāpēc apkopotas)",
	match:      [2]string{"atbilstība", "atbilstības"},
	andMore:    "…un vēl %d",
	minutes:    "%d min",

	firstListed:  "Pirmoreiz ievietots %s",
	inOneEarlier: ", 1 agrākā sludinājumā.", inEarlier: ", %d agrākos sludinājumos.",
	lastAskedNow: " Iepriekš prasīja %s, tagad %s (par %s %s).",
	more:         "vairāk", less: "mazāk", samePrice: " Cena tāda pati kā iepriekš.",
	lastAsked:  " Iepriekš prasīja %s.",
	dateLayout: "02.01.2006.",
	// Latvian: 1, 21, 31, ... but not 11 take the singular.
	pluralOne: func(n int) bool { return n%10 == 1 && n%100 != 11 },
}

// WordsIn are the notifier's words in lang; English for one there is none
// of, and for "" (an alert from before alerts had a language).
func WordsIn(lang string) *Words {
	if lang == events.LangLatvian {
		return wordsLv
	}
	return wordsEn
}

func (w *Words) matches(n int) string {
	if w.pluralOne(n) {
		return w.match[0]
	}
	return w.match[1]
}

func (w *Words) window(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf(w.minutes, int(d/time.Minute))
	}
	return d.String()
}

// siteZone is where the sites are, for dates in messages: the process runs
// in UTC, and an ad first seen just after midnight in Riga was seen "today".
var siteZone = func() *time.Location {
	if z, err := time.LoadLocation("Europe/Riga"); err == nil {
		return z
	}
	return time.UTC
}()

// RepostText says what was known about a reposted ad's item before, in
// lang: "First listed 3 Oct 2026, in 2 earlier ads. Last asked 8 900 €,
// now 8 500 € (400 € less)."
func RepostText(l events.ListingDiscovered, lang string) string {
	r := l.RepostOf
	if r == nil {
		return ""
	}
	w := WordsIn(lang)
	s := fmt.Sprintf(w.firstListed, r.FirstListed.In(siteZone).Format(w.dateLayout))
	if r.Times == 1 {
		s += w.inOneEarlier
	} else {
		s += fmt.Sprintf(w.inEarlier, r.Times)
	}
	switch {
	case r.PriceValue != nil && l.PriceValue != nil && *r.PriceValue != *l.PriceValue:
		diff := *l.PriceValue - *r.PriceValue
		change := w.more
		if diff < 0 {
			diff, change = -diff, w.less
		}
		s += fmt.Sprintf(w.lastAskedNow, textutil.Euros(*r.PriceValue), textutil.Euros(*l.PriceValue), textutil.Euros(diff), change)
	case r.PriceValue != nil && l.PriceValue != nil:
		s += w.samePrice
	case r.Price != "":
		s += fmt.Sprintf(w.lastAsked, strings.Join(strings.Fields(r.Price), " "))
	}
	return s
}
