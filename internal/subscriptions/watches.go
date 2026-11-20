package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions/subscriptionsdb"
)

// Price watches: a user pins one page, says where its price is (the
// dashboard's element picker) and when to hear about it. Pages are read on
// a schedule (CheckDueWatches), shared by everyone watching the same page
// and element; see watchcheck.go for how readings become alerts.

// WatchIntervals are the check intervals offered, in minutes.
var WatchIntervals = []int{30, 60, 180, 360, 720, 1440}

// Rules.
const (
	RuleBelow = "below" // alert at or below a target price
	RuleDrop  = "drop"  // alert after a drop of some percent from the reference price
)

// ErrWatchNotFound: no such watch, or not yours.
var ErrWatchNotFound = errors.New("watch not found")

// WatchRule is when a watch alerts.
type WatchRule struct {
	Kind        string   `json:"kind"`                  // RuleBelow or RuleDrop
	TargetCents *int64   `json:"targetCents,omitempty"` // RuleBelow
	DropPercent *float64 `json:"dropPercent,omitempty"` // RuleDrop, e.g. 5 for 5%
}

// Threshold is the price at or below which the rule alerts, given the
// watch's reference price. A 5% drop from 100.00 is 95.00.
func (r WatchRule) Threshold(reference int64) int64 {
	if r.Kind == RuleBelow && r.TargetCents != nil {
		return *r.TargetCents
	}
	if r.DropPercent == nil {
		return 0
	}
	basis := int64(math.Round(*r.DropPercent * 100)) // hundredths of a percent
	return reference * (10000 - basis) / 10000
}

func (r WatchRule) validate() error {
	switch r.Kind {
	case RuleBelow:
		if r.TargetCents == nil || *r.TargetCents <= 0 {
			return &sources.InvalidSearch{Msg: "enter the target price"}
		}
		if *r.TargetCents > 1e13 {
			return &sources.InvalidSearch{Msg: "that target price is too high"}
		}
	case RuleDrop:
		if r.DropPercent == nil || *r.DropPercent <= 0 || *r.DropPercent >= 100 {
			return &sources.InvalidSearch{Msg: "the drop must be between 0 and 100%"}
		}
		rounded := math.Round(*r.DropPercent*100) / 100 // the column keeps two decimals
		*r.DropPercent = rounded
	default:
		return &sources.InvalidSearch{Msg: "choose when to be alerted: below a price, or after a drop"}
	}
	return nil
}

// WatchInput is a watch as the user sets it up.
type WatchInput struct {
	Name            string
	URL             string
	Extractor       sources.Extractor
	Rule            WatchRule
	IntervalMinutes int
	ChannelID       int64 // 0 on update keeps the current one
	// Reading is what the page says now, read from the very copy the price
	// was picked on. It is the silent baseline: a new watch never alerts on
	// the price it started with. Needed on create, and when the page or
	// element changes.
	Reading *sources.Reading
}

// WatchView is a watch as shown to its owner.
type WatchView struct {
	ID              int64             `json:"id"`
	UserID          int64             `json:"userId"`
	UserName        string            `json:"userName"`
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	URL             string            `json:"url"`
	Extractor       sources.Extractor `json:"extractor"`
	Title           string            `json:"title"`
	ImageURL        string            `json:"imageUrl"`
	Rule            WatchRule         `json:"rule"`
	ThresholdCents  int64             `json:"thresholdCents"`
	ReferenceCents  int64             `json:"referenceCents"`
	ReferenceAt     time.Time         `json:"referenceAt"`
	Below           bool              `json:"below"` // at or below the threshold now
	IntervalMinutes int               `json:"intervalMinutes"`
	Channel         ChannelView       `json:"channel"`
	Alerts          int64             `json:"alerts"` // price alerts sent so far

	// The page, shared with anyone watching the same thing.
	Currency    string     `json:"currency"`
	PriceCents  *int64     `json:"priceCents"` // last valid price; kept while reads fail
	Raw         string     `json:"raw"`
	CheckedAt   *time.Time `json:"checkedAt"`
	OKAt        *time.Time `json:"okAt"`
	NextCheckAt time.Time  `json:"nextCheckAt"`
	Failures    int        `json:"failures"` // failed reads in a row
	Failing     bool       `json:"failing"`  // watchers were told it can't be read
	LastError   string     `json:"lastError"`
	// Skipped: a "check now" found the page read under half a minute ago
	// and showed that reading instead of reading it again.
	Skipped bool `json:"skipped,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	targetID int64
}

// WithWatchLimits sets how many watches one user may have (0 = unlimited;
// admins are exempt) and the shortest check interval offered, in minutes.
func (s *Service) WithWatchLimits(maxWatches, minIntervalMinutes int) *Service {
	s.maxWatches, s.minWatchInterval = maxWatches, minIntervalMinutes
	return s
}

// WithPublicURL is where the dashboard is, for links in alerts.
func (s *Service) WithPublicURL(u string) *Service {
	s.publicURL = strings.TrimRight(u, "/")
	return s
}

// AllowedWatchIntervals are the intervals a user may choose, in minutes.
func (s *Service) AllowedWatchIntervals() []int {
	var out []int
	for _, m := range WatchIntervals {
		if m >= s.minWatchInterval {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = WatchIntervals[len(WatchIntervals)-1:]
	}
	return out
}

func (s *Service) validateWatch(ctx context.Context, userID int64, in *WatchInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 80:
		return &sources.InvalidSearch{Msg: "give the watch a name (up to 80 characters)"}
	case !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") || len(in.URL) > 2000:
		return &sources.InvalidSearch{Msg: "the page's address must start with http:// or https://"}
	case !slices.Contains(s.AllowedWatchIntervals(), in.IntervalMinutes):
		return &sources.InvalidSearch{Msg: fmt.Sprintf("check every %v minutes", s.AllowedWatchIntervals())}
	}
	if err := in.Extractor.Validate(); err != nil {
		return err
	}
	if err := in.Rule.validate(); err != nil {
		return err
	}
	if in.Rule.Kind == RuleBelow {
		in.Rule.DropPercent = nil
	} else {
		in.Rule.TargetCents = nil
	}
	if in.Reading != nil {
		switch {
		case in.Reading.Price.Cents <= 0:
			return &sources.InvalidSearch{Msg: "the picked element has no price"}
		case in.Reading.Price.Currency == "":
			return &sources.InvalidSearch{Msg: "choose the price's currency"}
		}
	}
	if _, err := s.q.GetUserChannel(ctx, subscriptionsdb.GetUserChannelParams{ID: in.ChannelID, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &sources.InvalidSearch{Msg: "unknown alert channel"}
		}
		return err
	}
	return nil
}

// CreateWatch validates and saves a new watch, starting from in.Reading.
func (s *Service) CreateWatch(ctx context.Context, o Owner, in WatchInput) (WatchView, error) {
	if s.maxWatches > 0 && !o.Admin {
		n, err := s.q.CountUserWatches(ctx, o.UserID)
		if err != nil {
			return WatchView{}, err
		}
		if n >= int64(s.maxWatches) {
			return WatchView{}, &sources.InvalidSearch{Msg: fmt.Sprintf("you already have %d price watches, the limit; delete one first", n)}
		}
	}
	if in.ChannelID == 0 {
		return WatchView{}, &sources.InvalidSearch{Msg: "choose where alerts should go"}
	}
	if in.Reading == nil {
		return WatchView{}, &sources.InvalidSearch{Msg: "open the page and pick the price first"}
	}
	if err := s.validateWatch(ctx, o.UserID, &in); err != nil {
		return WatchView{}, err
	}
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		targetID, err := ensureTarget(ctx, q, in)
		if err != nil {
			return err
		}
		ref := in.Reading.Price.Cents
		id, err = q.InsertWatch(ctx, subscriptionsdb.InsertWatchParams{
			UserID: o.UserID, ChannelID: in.ChannelID, TargetID: targetID, Name: in.Name, Rule: in.Rule.Kind,
			TargetCents: in.Rule.TargetCents, DropPercent: in.Rule.DropPercent, ReferenceCents: ref,
			Below: ref <= in.Rule.Threshold(ref), IntervalMinutes: int32(in.IntervalMinutes),
		})
		return err
	})
	if err != nil {
		return WatchView{}, watchNameTaken(err, in.Name)
	}
	return s.GetWatch(ctx, o, id)
}

// ensureTarget returns the target for the page and element, creating it
// from in.Reading if nobody watches them yet.
func ensureTarget(ctx context.Context, q *subscriptionsdb.Queries, in WatchInput) (int64, error) {
	x, err := json.Marshal(in.Extractor)
	if err != nil {
		return 0, err
	}
	r := in.Reading
	price := r.Price.Cents
	id, err := q.InsertWatchTarget(ctx, subscriptionsdb.InsertWatchTargetParams{
		Url: in.URL, Extractor: x, Title: r.Title, ImageUrl: r.ImageURL, PriceCents: &price,
		Currency: r.Price.Currency, Raw: r.Raw, IntervalMinutes: int32(in.IntervalMinutes),
	})
	if err == nil {
		return id, q.InsertWatchPrice(ctx, subscriptionsdb.InsertWatchPriceParams{
			TargetID: id, Ok: true, PriceCents: &price, Currency: r.Price.Currency, Raw: r.Raw,
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if id, err = q.GetWatchTargetID(ctx, subscriptionsdb.GetWatchTargetIDParams{Url: in.URL, Extractor: x}); err != nil {
		return 0, err
	}
	return id, q.PullWatchTarget(ctx, subscriptionsdb.PullWatchTargetParams{ID: id, IntervalMinutes: int32(in.IntervalMinutes)})
}

// UpdateWatch replaces a watch's settings. A new page or element needs
// in.Reading and restarts the watch from it; otherwise the reference price
// stays and only the rule is re-applied to the last price, silently.
func (s *Service) UpdateWatch(ctx context.Context, o Owner, id int64, in WatchInput) (WatchView, error) {
	cur, err := s.GetWatch(ctx, o, id)
	if err != nil {
		return WatchView{}, err
	}
	if in.ChannelID == 0 {
		in.ChannelID = cur.Channel.ID
	}
	moved := strings.TrimSpace(in.URL) != cur.URL || in.Extractor != cur.Extractor
	if moved && in.Reading == nil {
		return WatchView{}, &sources.InvalidSearch{Msg: "open the page and pick the price again"}
	}
	if !moved {
		in.Reading = nil
	}
	if err := s.validateWatch(ctx, cur.UserID, &in); err != nil {
		return WatchView{}, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		p := subscriptionsdb.UpdateWatchParams{
			ID: id, ChannelID: in.ChannelID, TargetID: cur.targetID, Name: in.Name, Rule: in.Rule.Kind,
			TargetCents: in.Rule.TargetCents, DropPercent: in.Rule.DropPercent,
			ReferenceCents: cur.ReferenceCents, IntervalMinutes: int32(in.IntervalMinutes), Below: cur.Below,
		}
		if moved {
			if p.TargetID, err = ensureTarget(ctx, q, in); err != nil {
				return err
			}
			p.ReferenceCents, p.ResetReference = in.Reading.Price.Cents, true
			p.Below = p.ReferenceCents <= in.Rule.Threshold(p.ReferenceCents)
		} else {
			if cur.PriceCents != nil {
				p.Below = *cur.PriceCents <= in.Rule.Threshold(cur.ReferenceCents)
			}
			if err := q.PullWatchTarget(ctx, subscriptionsdb.PullWatchTargetParams{ID: cur.targetID, IntervalMinutes: int32(in.IntervalMinutes)}); err != nil {
				return err
			}
		}
		if _, err := q.UpdateWatch(ctx, p); err != nil {
			return err
		}
		if moved {
			return q.DeleteOrphanWatchTarget(ctx, cur.targetID)
		}
		return nil
	})
	if err != nil {
		return WatchView{}, watchNameTaken(err, in.Name)
	}
	return s.GetWatch(ctx, o, id)
}

// ResetWatchReference makes the last valid price the new reference (for a
// percentage rule: drops count from here on).
func (s *Service) ResetWatchReference(ctx context.Context, o Owner, id int64) (WatchView, error) {
	cur, err := s.GetWatch(ctx, o, id)
	if err != nil {
		return WatchView{}, err
	}
	if cur.PriceCents == nil {
		return WatchView{}, &sources.InvalidSearch{Msg: "the page has no price yet"}
	}
	ref := *cur.PriceCents
	_, err = s.q.UpdateWatch(ctx, subscriptionsdb.UpdateWatchParams{
		ID: id, ChannelID: cur.Channel.ID, TargetID: cur.targetID, Name: cur.Name, Rule: cur.Rule.Kind,
		TargetCents: cur.Rule.TargetCents, DropPercent: cur.Rule.DropPercent, ReferenceCents: ref, ResetReference: true,
		Below: ref <= cur.Rule.Threshold(ref), IntervalMinutes: int32(cur.IntervalMinutes),
	})
	if err != nil {
		return WatchView{}, err
	}
	return s.GetWatch(ctx, o, id)
}

// SetWatchEnabled pauses or resumes a watch.
func (s *Service) SetWatchEnabled(ctx context.Context, o Owner, id int64, enabled bool) error {
	cur, err := s.GetWatch(ctx, o, id)
	if err != nil {
		return err
	}
	if _, err := s.q.SetWatchEnabled(ctx, subscriptionsdb.SetWatchEnabledParams{ID: id, Enabled: enabled}); err != nil {
		return err
	}
	if enabled {
		return s.q.PullWatchTarget(ctx, subscriptionsdb.PullWatchTargetParams{ID: cur.targetID, IntervalMinutes: int32(cur.IntervalMinutes)})
	}
	return nil
}

// DeleteWatch removes a watch, and its page's history if nobody else
// watches that page.
func (s *Service) DeleteWatch(ctx context.Context, o Owner, id int64) error {
	if _, err := s.GetWatch(ctx, o, id); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		target, err := q.DeleteWatch(ctx, id)
		if err != nil {
			return err
		}
		return q.DeleteOrphanWatchTarget(ctx, target)
	})
}

// GetWatch returns one watch the owner may see.
func (s *Service) GetWatch(ctx context.Context, o Owner, id int64) (WatchView, error) {
	r, err := s.q.GetWatchView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !o.can(r.UserID)) {
		return WatchView{}, ErrWatchNotFound
	}
	if err != nil {
		return WatchView{}, err
	}
	return watchViewOf(subscriptionsdb.ListWatchViewsRow(r))
}

// ListWatches returns the owner's watches; everyone's for an admin who asks
// for all.
func (s *Service) ListWatches(ctx context.Context, o Owner, all bool) ([]WatchView, error) {
	rows, err := s.q.ListWatchViews(ctx, subscriptionsdb.ListWatchViewsParams{Everyone: all && o.Admin, UserID: o.UserID})
	if err != nil {
		return nil, err
	}
	out := make([]WatchView, 0, len(rows))
	for _, r := range rows {
		v, err := watchViewOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PricePoint is one entry of a page's price history: the price (or the
// failure) from At until the next point.
type PricePoint struct {
	At       time.Time `json:"at"`
	OK       bool      `json:"ok"`
	Cents    *int64    `json:"cents"`
	Currency string    `json:"currency"`
	Raw      string    `json:"raw"`
	Error    string    `json:"error"`
}

// WatchAlert is one alert a watch raised.
type WatchAlert struct {
	Kind  string    `json:"kind"` // drop, failing, recovered
	Cents *int64    `json:"cents"`
	At    time.Time `json:"at"`
	Sent  bool      `json:"sent"` // handed to the notifier
}

// WatchHistory is a watch's page history, newest first, and its alerts.
func (s *Service) WatchHistory(ctx context.Context, o Owner, id int64, limit int) ([]PricePoint, []WatchAlert, error) {
	w, err := s.GetWatch(ctx, o, id)
	if err != nil {
		return nil, nil, err
	}
	prices, err := s.q.ListWatchPrices(ctx, subscriptionsdb.ListWatchPricesParams{TargetID: w.targetID, MaxRows: int32(limit)})
	if err != nil {
		return nil, nil, err
	}
	alerts, err := s.q.ListWatchAlerts(ctx, subscriptionsdb.ListWatchAlertsParams{WatchID: id, MaxRows: int32(limit)})
	if err != nil {
		return nil, nil, err
	}
	points := make([]PricePoint, len(prices))
	for i, p := range prices {
		points[i] = PricePoint{At: p.At.Time, OK: p.Ok, Cents: p.PriceCents, Currency: p.Currency, Raw: p.Raw, Error: p.Error}
	}
	out := make([]WatchAlert, len(alerts))
	for i, a := range alerts {
		out[i] = WatchAlert{Kind: a.Kind, Cents: a.PriceCents, At: a.CreatedAt.Time, Sent: a.PublishedAt.Valid}
	}
	return points, out, nil
}

func watchViewOf(r subscriptionsdb.ListWatchViewsRow) (WatchView, error) {
	var x sources.Extractor
	if err := json.Unmarshal(r.Extractor, &x); err != nil {
		return WatchView{}, fmt.Errorf("watch %d extractor: %w", r.ID, err)
	}
	rule := WatchRule{Kind: r.Rule, TargetCents: r.TargetCents, DropPercent: r.DropPercent}
	v := WatchView{
		ID: r.ID, UserID: r.UserID, UserName: r.UserName, Name: r.Name, Enabled: r.Enabled, URL: r.Url,
		Extractor: x, Title: r.Title, ImageURL: r.ImageUrl, Rule: rule, ThresholdCents: rule.Threshold(r.ReferenceCents),
		ReferenceCents: r.ReferenceCents, ReferenceAt: r.ReferenceAt.Time, Below: r.Below,
		IntervalMinutes: int(r.IntervalMinutes), Alerts: r.Alerts,
		Channel:  ChannelView{ID: r.ChannelID, Kind: r.ChannelKind, Target: r.ChannelTarget, Label: r.ChannelLabel},
		Currency: r.Currency, PriceCents: r.PriceCents, Raw: r.Raw, CheckedAt: timePtr(r.CheckedAt), OKAt: timePtr(r.OkAt),
		NextCheckAt: r.NextCheckAt.Time, Failures: int(r.Failures), Failing: r.Failing, LastError: r.LastError,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, targetID: r.TargetID,
	}
	return v, nil
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func watchNameTaken(err error, name string) error {
	var inv *sources.InvalidSearch
	if errors.As(nameTaken(err, name), &inv) {
		return &sources.InvalidSearch{Msg: fmt.Sprintf("you already have a watch named %q", name)}
	}
	return err
}
