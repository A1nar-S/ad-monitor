package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/textutil"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions/subscriptionsdb"
)

const (
	// readLease is how long a claimed target is someone's to read.
	readLease = 10 * time.Minute
	// failAlertAfter failed reads in a row tell the watchers the page can't
	// be read; one failure is usually a blip.
	failAlertAfter = 3
	// retryAfter is the first retry after a failed read; it doubles per
	// failure up to the watch's own interval.
	retryAfter = 10 * time.Minute
)

// Alert kinds.
const (
	AlertDrop      = "drop"
	AlertFailing   = "failing"
	AlertRecovered = "recovered"
)

// CheckDueWatches reads up to max pages that are due and applies every
// watch on them. It returns how many pages it read.
func (s *Service) CheckDueWatches(ctx context.Context, r sources.PageReader, max int) (int, error) {
	due, err := s.q.ClaimDueWatchTargets(ctx, subscriptionsdb.ClaimDueWatchTargetsParams{
		LeaseSeconds: readLease.Seconds(), MaxRows: int32(max),
	})
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, t := range due {
		if err := s.checkTarget(ctx, r, t.ID, t.Url, t.Extractor); err != nil {
			errs = append(errs, fmt.Errorf("target %d: %w", t.ID, err))
		}
	}
	return len(due), errors.Join(errs...)
}

// CheckWatchNow reads a watch's page right away (the dashboard's "Check
// now"), unless it was read in the last half minute.
func (s *Service) CheckWatchNow(ctx context.Context, o Owner, id int64, r sources.PageReader) (WatchView, error) {
	w, err := s.GetWatch(ctx, o, id)
	if err != nil {
		return WatchView{}, err
	}
	t, err := s.q.ClaimWatchTarget(ctx, subscriptionsdb.ClaimWatchTargetParams{
		ID: w.targetID, LeaseSeconds: readLease.Seconds(), MinAgeSeconds: 30,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		w.Skipped = true // just read; nothing new to see
		return w, nil
	}
	if err != nil {
		return WatchView{}, err
	}
	if err := s.checkTarget(ctx, r, t.ID, t.Url, t.Extractor); err != nil {
		return WatchView{}, err
	}
	return s.GetWatch(ctx, o, id)
}

// checkTarget reads one page and records what it said. Only database
// errors are returned: a page that can't be read is a recorded failure.
func (s *Service) checkTarget(ctx context.Context, r sources.PageReader, id int64, url string, rawX []byte) error {
	var x sources.Extractor
	if err := json.Unmarshal(rawX, &x); err != nil {
		return err
	}
	prev, err := s.q.GetWatchTargetState(ctx, id)
	if err != nil {
		return err
	}
	start := time.Now()
	reading, readErr := r.ReadPrice(ctx, url, x)
	if readErr == nil {
		readErr = s.validateReading(ctx, r, url, x, prev, &reading)
	}
	if readErr != nil {
		slog.Info("watch read failed", "target", id, "url", textutil.OneLine(url), "err", textutil.OneLine(readErr.Error()))
	}
	err = s.recordReading(ctx, id, reading, readErr)
	if err == nil && ctx.Err() == nil {
		watchChecks.WithLabelValues(watchResult(readErr)).Inc()
		watchCheckDuration.Observe(time.Since(start).Seconds())
	}
	return err
}

// validateReading guards the alerts against misreads: a price in another
// currency is not comparable, and a price under half the last one is read
// a second time before anyone hears of it.
func (s *Service) validateReading(ctx context.Context, r sources.PageReader, url string, x sources.Extractor,
	prev subscriptionsdb.GetWatchTargetStateRow, reading *sources.Reading) error {
	if prev.Currency != "" && reading.Price.Currency != prev.Currency {
		return fmt.Errorf("%w: the price is in %s now, it was in %s", sources.ErrNoPrice, orNone(reading.Price.Currency), prev.Currency)
	}
	if prev.PriceCents == nil || reading.Price.Cents*2 >= *prev.PriceCents {
		return nil
	}
	again, err := r.ReadPrice(ctx, url, x)
	if err != nil {
		return fmt.Errorf("the price fell to %s, but reading it again failed: %w", reading.Price, err)
	}
	if again.Price != reading.Price {
		return fmt.Errorf("%w: two reads disagree (%s, then %s)", sources.ErrNoPrice, reading.Price, again.Price)
	}
	return nil
}

func orNone(currency string) string {
	if currency == "" {
		return "no currency"
	}
	return currency
}

// recordReading stores a reading (or a failed read) and raises the alerts
// it causes, in one transaction: the history, the watches' state and the
// alert outbox never disagree.
func (s *Service) recordReading(ctx context.Context, id int64, reading sources.Reading, readErr error) error {
	// Counted only once the transaction committed.
	var raised []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		raised = raised[:0]
		q := s.q.WithTx(tx)
		t, err := q.LockWatchTarget(ctx, id)
		if err != nil {
			return err
		}
		watches, err := q.ListTargetWatches(ctx, id)
		if err != nil {
			return err
		}
		interval := time.Hour
		for i, w := range watches {
			if d := time.Duration(w.IntervalMinutes) * time.Minute; i == 0 || d < interval {
				interval = d
			}
		}

		if readErr != nil {
			return s.recordFailure(ctx, q, t, watches, interval, readErr, &raised)
		}

		cents := reading.Price.Cents
		if t.PriceCents == nil || *t.PriceCents != cents || t.Currency != reading.Price.Currency || t.Failures > 0 {
			err := q.InsertWatchPrice(ctx, subscriptionsdb.InsertWatchPriceParams{
				TargetID: id, Ok: true, PriceCents: &cents, Currency: reading.Price.Currency, Raw: reading.Raw,
			})
			if err != nil {
				return err
			}
		}
		err = q.SaveWatchTargetOK(ctx, subscriptionsdb.SaveWatchTargetOKParams{
			ID: id, PriceCents: &cents, Currency: reading.Price.Currency, Raw: reading.Raw,
			Title: reading.Title, ImageUrl: reading.ImageURL, NextInSeconds: jitter(interval).Seconds(),
		})
		if err != nil {
			return err
		}

		for _, w := range watches {
			rule := WatchRule{Kind: w.Rule, TargetCents: w.TargetCents, DropPercent: w.DropPercent}
			below := cents <= rule.Threshold(w.ReferenceCents)
			if below == w.Below {
				continue
			}
			// Crossing down alerts; rising above again only re-arms.
			if err := q.SetWatchBelow(ctx, subscriptionsdb.SetWatchBelowParams{ID: w.ID, Below: below}); err != nil {
				return err
			}
			if below {
				text := s.dropText(w, rule, t, reading.Price)
				if err := insertAlert(ctx, q, w, AlertDrop, &cents, text, &raised); err != nil {
					return err
				}
			}
		}
		if t.Failing {
			for _, w := range watches {
				text := fmt.Sprintf("✅ **%s**: reading the price works again: **%s**.\n%s", w.Name, reading.Price, t.Url)
				if err := insertAlert(ctx, q, w, AlertRecovered, &cents, text, &raised); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		for _, kind := range raised {
			watchAlerts.WithLabelValues(kind).Inc()
		}
	}
	return err
}

func (s *Service) recordFailure(ctx context.Context, q *subscriptionsdb.Queries, t subscriptionsdb.LockWatchTargetRow,
	watches []subscriptionsdb.ListTargetWatchesRow, interval time.Duration, readErr error, raised *[]string) error {
	msg := clipText(readErr.Error(), 300)
	if t.Failures == 0 {
		err := q.InsertWatchPrice(ctx, subscriptionsdb.InsertWatchPriceParams{TargetID: t.ID, Ok: false, Currency: t.Currency, Error: msg})
		if err != nil {
			return err
		}
	}
	failures := t.Failures + 1
	failing := t.Failing || failures >= failAlertAfter
	next := min(retryAfter<<min(failures-1, 8), interval)
	err := q.SaveWatchTargetFailure(ctx, subscriptionsdb.SaveWatchTargetFailureParams{
		ID: t.ID, Failures: failures, Failing: failing, LastError: msg, NextInSeconds: jitter(next).Seconds(),
	})
	if err != nil || t.Failing || !failing {
		return err
	}
	last := "no valid price yet"
	if t.PriceCents != nil {
		last = fmt.Sprintf("last price %s on %s", sources.FormatCents(*t.PriceCents, t.Currency), t.OkAt.Time.UTC().Format("2 Jan 15:04 MST"))
	}
	for _, w := range watches {
		text := fmt.Sprintf("⚠️ **%s**: can't read the price any more: %s (%d tries). Its %s.\n"+
			"Alerts resume by themselves when the page reads again. If the page changed, pick the price again: %s",
			w.Name, failureReason(readErr), failures, last, s.dashboardLink(w.ID))
		if err := insertAlert(ctx, q, w, AlertFailing, nil, text, raised); err != nil {
			return err
		}
	}
	return nil
}

// dropText is the message of a price alert.
func (s *Service) dropText(w subscriptionsdb.ListTargetWatchesRow, rule WatchRule, t subscriptionsdb.LockWatchTargetRow, now sources.Price) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📉 **%s**: now **%s**", w.Name, now)
	if t.PriceCents != nil && *t.PriceCents != now.Cents {
		fmt.Fprintf(&b, " (was %s)", sources.FormatCents(*t.PriceCents, now.Currency))
	}
	b.WriteString("\n")
	if rule.Kind == RuleBelow {
		fmt.Fprintf(&b, "At or below your target of %s.", sources.FormatCents(*rule.TargetCents, now.Currency))
	} else {
		off := float64(w.ReferenceCents-now.Cents) * 100 / float64(w.ReferenceCents)
		fmt.Fprintf(&b, "%s%% below your reference price of %s (you asked for %s%%).",
			strconv.FormatFloat(off, 'f', 1, 64), sources.FormatCents(w.ReferenceCents, now.Currency),
			strconv.FormatFloat(*rule.DropPercent, 'f', -1, 64))
	}
	b.WriteString("\n" + t.Url)
	return b.String()
}

func (s *Service) dashboardLink(watchID int64) string {
	if s.publicURL == "" {
		return "the dashboard"
	}
	return fmt.Sprintf("%s/watches/%d", s.publicURL, watchID)
}

// failureReason says in a few words why a page could not be read.
func failureReason(err error) string {
	switch {
	case errors.Is(err, sources.ErrPageGone):
		return "the page is gone"
	case errors.Is(err, sources.ErrBlocked):
		return "the site refuses the request"
	case errors.Is(err, sources.ErrNoPrice):
		return "the price is not where it was (" + clipText(err.Error(), 120) + ")"
	}
	return "the site can't be reached"
}

func insertAlert(ctx context.Context, q *subscriptionsdb.Queries, w subscriptionsdb.ListTargetWatchesRow, kind string, cents *int64, text string, raised *[]string) error {
	payload, err := json.Marshal(events.NotificationRequested{
		UserID: w.UserID, Channel: events.Channel{Kind: w.ChannelKind, Target: w.ChannelTarget}, Text: text,
	})
	if err != nil {
		return err
	}
	err = q.InsertWatchAlert(ctx, subscriptionsdb.InsertWatchAlertParams{WatchID: w.ID, Kind: kind, PriceCents: cents, Payload: payload})
	if err == nil {
		*raised = append(*raised, kind)
	}
	return err
}

// RelayWatchAlerts hands up to batch unsent alerts to publish, oldest
// first, and marks the ones that went out. Each alert's idempotency key is
// its row, so a crash after publishing re-sends nothing the notifier
// hasn't deduplicated. Returns how many were published.
func (s *Service) RelayWatchAlerts(ctx context.Context, batch int32, publish func(context.Context, events.NotificationRequested) error) (int, error) {
	var sent []int64
	var pubErr error
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.ClaimWatchAlerts(ctx, batch)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var n events.NotificationRequested
			if err := json.Unmarshal(r.Payload, &n); err != nil {
				slog.Error("bad watch alert payload, dropping", "id", r.ID, "err", err)
			} else {
				n.Key = fmt.Sprintf("watch-alert:%d", r.ID)
				if pubErr = publish(ctx, n); pubErr != nil {
					break
				}
			}
			sent = append(sent, r.ID)
		}
		if len(sent) == 0 {
			return nil
		}
		return q.MarkWatchAlertsPublished(ctx, sent)
	})
	if err != nil {
		return 0, err
	}
	return len(sent), pubErr
}

// jitter spreads reads by up to 5% (at most two minutes), so pages added
// together are not read together forever.
func jitter(d time.Duration) time.Duration {
	spread := min(d/20, 2*time.Minute)
	if spread <= 0 {
		return d
	}
	return d + rand.N(spread)
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
