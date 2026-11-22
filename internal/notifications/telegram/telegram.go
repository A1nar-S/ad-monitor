// Package telegram delivers notifications to Telegram chats through the Bot
// API: a listing as a photo with a caption, or as text when it has none.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/notifications"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/telegram"
)

// Kind is the channel kind Telegram chats have.
const Kind = "telegram"

// Telegram's limits, in characters of visible text.
const (
	maxCaption = 1024
	maxText    = 4096
)

// Notifier posts to any chat the bot may write to; one bot serves every user.
type Notifier struct{ c *telegram.Client }

func New(c *telegram.Client) *Notifier { return &Notifier{c: c} }

func (n *Notifier) Kind() string { return Kind }

func (n *Notifier) NotifyText(ctx context.Context, chat, msg string) error {
	_, err := n.c.SendMessage(ctx, chat, telegram.HTML(msg), nil, false)
	return classify(err)
}

// NotifyListing sends the ad's photo with the facts as its caption. A photo
// Telegram can't fetch falls back to a text message.
func (n *Notifier) NotifyListing(ctx context.Context, chat string, l events.ListingDiscovered) error {
	if l.ImageURL != "" {
		err := n.c.SendPhoto(ctx, chat, l.ImageURL, listingHTML(l, maxCaption))
		var te *telegram.Error
		if err == nil || !errors.As(err, &te) || te.Code != http.StatusBadRequest || telegram.Undeliverable(err) {
			return classify(err)
		}
	}
	_, err := n.c.SendMessage(ctx, chat, listingHTML(l, maxText), nil, true)
	return classify(err)
}

func classify(err error) error {
	if telegram.Undeliverable(err) {
		return fmt.Errorf("%w: %w", notifications.ErrUndeliverable, err)
	}
	return err
}

// listingHTML renders a listing in at most limit visible characters,
// leaving out the source's facts that don't fit.
func listingHTML(l events.ListingDiscovered, limit int) string {
	var c caption
	heading := "🆕 New listing"
	if l.RepostOf != nil {
		heading = "♻️ Reposted listing"
	}
	c.line("<b>"+heading+"</b>", heading)
	if t := strings.TrimSpace(l.Title); t != "" {
		t = truncate(strings.Join(strings.Fields(t), " "), 300)
		c.line(html.EscapeString(t), t)
	}
	if l.Price != "" {
		c.line("💰 <b>"+html.EscapeString(l.Price)+"</b>", "💰 "+l.Price)
	}
	if l.RepostOf != nil {
		t := "♻️ " + notifications.RepostText(l)
		c.line(html.EscapeString(t), t)
	}
	var tail caption
	if l.URL != "" {
		t := "📎 View on " + l.Source
		tail.line(`<a href="`+html.EscapeString(l.URL)+`">`+html.EscapeString(t)+"</a>", t)
	}
	if l.ExternalID != "" {
		tail.line("ID: "+html.EscapeString(l.ExternalID), "ID: "+l.ExternalID)
	}
	for _, f := range l.Fields {
		t := f.Name + ": " + f.Value
		if c.n+1+utf8.RuneCountInString(t)+1+tail.n > limit {
			continue
		}
		c.line("<b>"+html.EscapeString(f.Name)+"</b>: "+html.EscapeString(f.Value), t)
	}
	if c.n+1+tail.n > limit {
		return c.html.String()
	}
	return c.html.String() + "\n" + tail.html.String()
}

// caption builds HTML lines and counts their visible characters.
type caption struct {
	html strings.Builder
	n    int
}

func (c *caption) line(h, visible string) {
	if c.html.Len() > 0 {
		c.html.WriteString("\n")
		c.n++
	}
	c.html.WriteString(h)
	c.n += utf8.RuneCountInString(visible)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
