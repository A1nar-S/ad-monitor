package web

// Writing responses.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// fail writes err as a response: a user's mistake is 422 with its message.
func (s *server) fail(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var inv *sources.InvalidSearch
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusUnprocessableEntity, inv.Msg)
	case errors.Is(err, subscriptions.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such search")
	case errors.Is(err, subscriptions.ErrWatchNotFound):
		writeError(w, http.StatusNotFound, "no such watch")
	default:
		slog.Error("dashboard api", "err", err)
		writeError(w, http.StatusInternalServerError, "something went wrong")
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
