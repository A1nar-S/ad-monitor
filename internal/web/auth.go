package web

// Login, sessions and who may call what.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// login trades the bot's one-time link for a session cookie.
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	// Chat apps fetch links to build previews; that GET must not use up the
	// single-use token before the person clicks it.
	if linkPreviewer(r.UserAgent()) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	session, _, err := s.Searches.Login(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		if !errors.Is(err, subscriptions.ErrBadToken) {
			slog.Error("login", "err", err)
		}
		http.Redirect(w, r, "/?login=expired", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: session, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(subscriptions.SessionTTL / time.Second),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// linkPreviewer reports whether a User-Agent is a chat app's link-preview fetcher.
func linkPreviewer(ua string) bool {
	ua = strings.ToLower(ua)
	for _, bot := range []string{"discordbot", "slackbot", "telegrambot", "twitterbot", "facebookexternalhit", "whatsapp", "skypeuripreview"} {
		if strings.Contains(ua, bot) {
			return true
		}
	}
	return false
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Searches.Logout(r.Context(), c.Value); err != nil {
			slog.Error("logout", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secure, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

type ownerHandler func(http.ResponseWriter, *http.Request, subscriptions.Owner)

// auth resolves the session and, for anything but reads, checks the CSRF
// header.
func (s *server) auth(h ownerHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Header.Get(csrfHeader) == "" {
			writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		o, err := s.Searches.SessionOwner(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, subscriptions.ErrBadToken) {
				slog.Error("session", "err", err)
				writeError(w, http.StatusServiceUnavailable, "try again in a moment")
				return
			}
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), ctxKey{}, o), 10*time.Second)
		defer cancel()
		h(w, r.WithContext(ctx), o)
	})
}

func (s *server) me(w http.ResponseWriter, _ *http.Request, o subscriptions.Owner) {
	me := map[string]any{
		"name": o.Name, "platform": o.Platform, "admin": o.Admin, "maxSearches": s.MaxSearches,
		"maxWatches": s.MaxWatches, "watchIntervals": []int{},
		"gems": o.Admin && s.Gems != nil && s.GemSource != "",
	}
	if s.Watches != nil {
		me["watchIntervals"] = s.Watches.AllowedWatchIntervals()
	}
	writeJSON(w, http.StatusOK, me)
}

// admin lets only admins through.
func (s *server) admin(h ownerHandler) ownerHandler {
	return func(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
		if !o.Admin {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		h(w, r, o)
	}
}
