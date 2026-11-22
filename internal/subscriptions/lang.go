package subscriptions

import (
	"context"
	"slices"
	"strings"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions/subscriptionsdb"
)

// AlertLang is the language the owner's alerts are written in:
// events.DefaultLang until they choose.
func (s *Service) AlertLang(ctx context.Context, o Owner) (string, error) {
	lang, err := s.q.GetAlertLang(ctx, o.UserID)
	return alertLangOr(lang), err
}

// alertLangOr is a stored alert language, or the default for a user who
// never chose (”).
func alertLangOr(stored string) string {
	if stored == "" {
		return events.DefaultLang
	}
	return stored
}

// SetAlertLang sets the language of the owner's alerts, one of
// events.AlertLangs.
func (s *Service) SetAlertLang(ctx context.Context, o Owner, lang string) error {
	if !slices.Contains(events.AlertLangs, lang) {
		return &InvalidLang{Lang: lang}
	}
	return s.q.SetAlertLang(ctx, subscriptionsdb.SetAlertLangParams{ID: o.UserID, AlertLang: lang})
}

// InvalidLang is an alert language there is none of; its message is meant
// for the user.
type InvalidLang struct{ Lang string }

func (e *InvalidLang) Error() string {
	return "alerts can be written in " + strings.Join(events.AlertLangs, ", ") + ", not " + e.Lang
}
