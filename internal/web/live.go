package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
)

const (
	// liveGap is how often one user may ask a site for a live preview: it is
	// a real request to someone else's site, made on a button press.
	liveGap = 10 * time.Second
	// liveSiteGap spaces live previews of one site across all users, so a
	// few people pressing at once cannot burst it from the scraper's IP.
	liveSiteGap = 3 * time.Second
	// liveFeeds caps the feeds one live preview reads (an ss.com flat search
	// can watch several districts); page 1 of each.
	liveFeeds = 3
	// liveTimeout fits within the server's write timeout.
	liveTimeout = 10 * time.Second
)

type liveLimiter struct {
	mu     sync.Mutex
	byUser map[int64]time.Time
	bySite map[string]time.Time
}

// allow reports whether user may read source now, and if so counts it.
func (l *liveLimiter) allow(user int64, source string, now time.Time) (ok, siteBusy bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byUser == nil {
		l.byUser, l.bySite = map[int64]time.Time{}, map[string]time.Time{}
	}
	if t, seen := l.byUser[user]; seen && now.Sub(t) < liveGap {
		return false, false
	}
	if t, seen := l.bySite[source]; seen && now.Sub(t) < liveSiteGap {
		return false, true
	}
	l.byUser[user], l.bySite[source] = now, now
	return true, false
}

// httpStatuser is a source's error for a refused request (ss and rrr have one).
type httpStatuser interface{ HTTPStatus() int }

// previewLive reads the newest ads of a search, as it is being edited, from
// its site right now and shows which of them match. Nothing is saved: the
// scraper stores ads only for saved searches.
func (s *server) previewLive(w http.ResponseWriter, r *http.Request, o subscriptions.Owner) {
	b, ok := readBody(w, r)
	if !ok {
		return
	}
	in := subscriptions.SearchInput{Source: b.Source, Category: b.Category, Filters: b.Filters}
	source, feeds, err := s.Searches.CompileFeeds(in)
	if s.fail(w, err) {
		return
	}
	src := s.Sources.Lookup(source)
	if src == nil {
		writeError(w, http.StatusUnprocessableEntity, source+" can't be read live")
		return
	}
	if ok, busy := s.live.allow(o.UserID, source, time.Now()); !ok {
		msg := "Wait a few seconds before checking the site again."
		if busy {
			msg = "Someone just checked " + source + "; try again in a few seconds."
		}
		writeError(w, http.StatusTooManyRequests, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), liveTimeout)
	defer cancel()
	in.Source = source
	var candidates []events.ListingDiscovered
	seen := map[string]bool{}
	for i, feed := range feeds[:min(len(feeds), liveFeeds)] {
		if i > 0 && !sleep(ctx, time.Second) { // space a search's feeds, like the scraper does
			break
		}
		ls, err := src.FetchFeed(ctx, feed, 1)
		if err != nil {
			var hs httpStatuser
			msg := fmt.Sprintf("%s didn't answer; try again later.", source)
			if errors.As(err, &hs) {
				msg = fmt.Sprintf("%s refused the request (status %d); try again later.", source, hs.HTTPStatus())
			}
			writeError(w, http.StatusBadGateway, msg)
			return
		}
		for _, l := range ls {
			if seen[l.ExternalID] {
				continue
			}
			seen[l.ExternalID] = true
			l.Source, l.Feed = source, feed // as the scraper's runner does
			candidates = append(candidates, l)
		}
	}
	// Newest first across feeds, as the stored preview lists them.
	slices.SortStableFunc(candidates, func(a, b events.ListingDiscovered) int { return b.PostedAt.Compare(a.PostedAt) })

	p, err := s.Searches.Preview(in, candidates)
	if s.fail(w, err) {
		return
	}
	out := previewView{
		Live: true, Scanned: len(candidates), InScope: p.InScope, Matched: len(p.Matched), Unsure: len(p.Unsure),
		UnsureKeys: nonNil(p.UnsureKeys), Ads: []previewAd{},
	}
	sure := make(map[int]bool, len(p.Matched))
	for _, i := range p.Matched {
		sure[i] = true
	}
	picked := append(append([]int(nil), p.Matched...), p.Unsure...)
	slices.Sort(picked)
	for _, i := range picked[:min(len(picked), previewShown)] {
		l := candidates[i]
		out.Ads = append(out.Ads, previewAd{
			// Not stored, so no listing id: the position keeps the list's keys unique.
			ID: int64(i + 1), Title: l.Title, URL: l.URL, Price: l.Price, ImageURL: l.ImageURL,
			Location: l.Location, FirstSeen: l.PostedAt, Active: true, Sure: sure[i],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// sleep waits d, or reports false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
