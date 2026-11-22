package subscriptions

import (
	"errors"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
)

// watchWords are the price-watch alerts in one language. Format verbs as
// the English comments show.
type watchWords struct {
	now            string // name, price: "📉 **%s**: now **%s**"
	was            string // price
	atTarget       string // target
	belowReference string // percent off, reference, percent asked
	recovered      string // name, price, url
	failing        string // name, reason, tries, last, link
	noPrice        string
	lastPrice      string // price, when
	when           string // time layout
	dashboard      string
	gone, blocked  string
	noPriceAt      string // what the reader said
	unreachable    string
	comma          bool // decimal comma
}

var watchEn = &watchWords{
	now:            "📉 **%s**: now **%s**",
	was:            " (was %s)",
	atTarget:       "At or below your target of %s.",
	belowReference: "%s%% below your reference price of %s (you asked for %s%%).",
	recovered:      "✅ **%s**: reading the price works again: **%s**.\n%s",
	failing: "⚠️ **%s**: can't read the price any more: %s (%d tries). Its %s.\n" +
		"Alerts resume by themselves when the page reads again. If the page changed, pick the price again: %s",
	noPrice:     "no valid price yet",
	lastPrice:   "last price %s on %s",
	when:        "2 Jan 15:04 MST",
	dashboard:   "the dashboard",
	gone:        "the page is gone",
	blocked:     "the site refuses the request",
	noPriceAt:   "the price is not where it was (%s)",
	unreachable: "the site can't be reached",
}

var watchLv = &watchWords{
	now:            "📉 **%s**: tagad **%s**",
	was:            " (bija %s)",
	atTarget:       "Jūsu mērķa cena %s vai zemāk.",
	belowReference: "Par %s%% zem jūsu atsauces cenas %s (jūs prasījāt %s%%).",
	recovered:      "✅ **%s**: cenu atkal izdodas nolasīt: **%s**.\n%s",
	failing: "⚠️ **%s**: cenu vairs neizdodas nolasīt: %s (%d mēģinājumi). Tai %s.\n" +
		"Paziņojumi atsāksies paši, kad lapu atkal izdosies nolasīt. Ja lapa mainījusies, izvēlieties cenu no jauna: %s",
	noPrice:     "vēl nav derīgas cenas",
	lastPrice:   "pēdējā cena %s, %s",
	when:        "02.01. 15:04 MST",
	dashboard:   "panelī",
	gone:        "lapa vairs nepastāv",
	blocked:     "vietne atsaka pieprasījumu",
	noPriceAt:   "cena vairs nav tur, kur bija (%s)",
	unreachable: "vietne nav sasniedzama",
	comma:       true,
}

// watchPhrases are the alerts in a stored alert language (” = the
// default); English for one there is none of.
func watchPhrases(stored string) *watchWords {
	if alertLangOr(stored) == events.LangLatvian {
		return watchLv
	}
	return watchEn
}

// decimal writes a number written with a point in the language's way.
func (p *watchWords) decimal(s string) string {
	if p.comma {
		return strings.Replace(s, ".", ",", 1)
	}
	return s
}

// reason says in a few words why a page could not be read.
func (p *watchWords) reason(err error) string {
	switch {
	case errors.Is(err, sources.ErrPageGone):
		return p.gone
	case errors.Is(err, sources.ErrBlocked):
		return p.blocked
	case errors.Is(err, sources.ErrNoPrice):
		return strings.Replace(p.noPriceAt, "%s", clipText(err.Error(), 120), 1)
	}
	return p.unreachable
}
