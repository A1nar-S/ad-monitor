package web

// Stored ads, for admins.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

// publishTimeout bounds what a request waits for Kafka.
const publishTimeout = 5 * time.Second

// keepAdPage hands an ad page's facts to ingest, waiting at most
// publishTimeout.
func (s *server) keepAdPage(ctx context.Context, p events.AdPageRead) error {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	return s.KeepAdPage(ctx, p)
}

// listingDetails reads one stored ad's own page now (for an admin, from the
// overview), hands what it adds to ingest and returns the listing's attrs.
func (s *server) listingDetails(w http.ResponseWriter, r *http.Request, _ subscriptions.Owner) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such listing")
		return
	}
	st, err := s.Listings.Get(r.Context(), id)
	if errors.Is(err, listings.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such listing")
		return
	}
	if s.fail(w, err) {
		return
	}
	src, _ := s.Sources.Lookup(st.Listing.Source).(sources.Enricher)
	if src == nil {
		writeError(w, http.StatusUnprocessableEntity, st.Listing.Source+" ads have no more details to read")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	enriched, err := src.Enrich(ctx, st.Listing)
	switch {
	case errors.Is(err, sources.ErrGone):
		if s.fail(w, s.keepAdPage(r.Context(), events.AdPageRead{Listing: st.Listing, Gone: true})) {
			return
		}
		writeError(w, http.StatusGone, "This ad has been removed from "+st.Listing.Source+".")
		return
	case err != nil:
		slog.Warn("read ad page", "listing", id, "err", err)
		writeError(w, http.StatusBadGateway, "Couldn't read the ad page on "+st.Listing.Source+". Try again in a moment.")
		return
	}
	if s.fail(w, s.keepAdPage(r.Context(), events.AdPageRead{Listing: st.Listing, Read: &enriched})) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attrs": enriched.Attrs, "active": st.Active, "detailedAt": time.Now().UTC(),
	})
}
