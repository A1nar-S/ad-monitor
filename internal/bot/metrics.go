package bot

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

var commandsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "admon_bot_commands_total",
	Help: "Bot commands and menu actions by command and result (ok, rejected: the user's mistake, error).",
}, []string{"command", "result"})

// commandLabel keeps only the bot's own command names, so what a client
// sends can never create series.
func commandLabel(name string) string {
	switch name {
	case CmdWatch, CmdEdit, CmdSearches, CmdMatches, CmdUnwatch, CmdDashboard, CmdSetup, CmdLink, CmdLanguage:
		return name
	case ActionLink:
		return "menu_link"
	case ActionToggle, ActionDelete:
		return "menu_" + name
	}
	return "other"
}

func observeCommand(name string, err error) {
	result := "ok"
	var inv *sources.InvalidSearch
	switch {
	case err == nil:
	case errors.As(err, &inv), errors.Is(err, subscriptions.ErrNotFound), errors.Is(err, subscriptions.ErrNotAdmin),
		errors.Is(err, subscriptions.ErrBadToken), errors.Is(err, subscriptions.ErrSameUser):
		result = "rejected"
	default:
		result = "error"
	}
	commandsTotal.WithLabelValues(commandLabel(name), result).Inc()
}
