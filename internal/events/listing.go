// Package events holds the contracts that cross slice boundaries. It is the
// only package every slice may import. Keep it free of logic: these structs
// are the Kafka message payloads (JSON for now).
package events

import "time"

// Kafka topics connecting the services. Each table has one writing service;
// the others ask it to write through these.
const (
	TopicListingsDiscovered     = "listings.discovered"     // scraper -> ingest
	TopicListingsNew            = "listings.new"            // ingest -> matcher
	TopicListingDetails         = "listings.details"        // matcher/api/scraper -> ingest
	TopicNotificationsRequested = "notifications.requested" // matcher/api/scraper -> notifier
	TopicGemScansRequested      = "gems.scan_requested"     // api -> scraper
)

// ListingDiscovered is emitted by a source for every ad found on a feed page,
// whether or not it has been seen before.
//
// It is deliberately source-neutral so a new site (city24, vinted, ...) needs
// no change here: what is specific to a site goes in Attrs (for filtering)
// and Fields (for display).
type ListingDiscovered struct {
	Source     string `json:"source"`      // e.g. "ss.com"
	ExternalID string `json:"external_id"` // ID on the source site, unique per source
	// Feed is the feed this ad was scraped from. Ingest uses it to store a
	// brand-new feed's existing ads without notifying anyone.
	Feed string `json:"feed"`
	// Location is a source-defined slug (e.g. "riga/centre"). Searches never
	// interpret it; the source's InScope does.
	Location string `json:"location,omitempty"`

	Title      string   `json:"title"`
	URL        string   `json:"url"`
	ImageURL   string   `json:"image_url,omitempty"`
	Price      string   `json:"price,omitempty"` // as displayed, e.g. "290 €/mēn."
	PriceValue *float64 `json:"price_value,omitempty"`

	// Attrs are the filterable facts: numbers (float64) or strings, e.g.
	// {"rooms": 2, "series": "Jaun."}. A search filters on them by key.
	Attrs map[string]any `json:"attrs,omitempty"`
	// Fields are the facts to show in a notification, in order. Only the
	// source knows which ones matter, so notifiers render them blindly.
	Fields []Field `json:"fields,omitempty"`

	PostedAt time.Time `json:"posted_at"`

	// Fingerprint identifies the item rather than the ad (a car by make,
	// model, year, engine and mileage): a seller who deletes an ad and posts
	// it again gets a new ExternalID but the same Fingerprint. "" = the
	// source cannot tell.
	Fingerprint string `json:"fingerprint,omitempty"`
	// RepostOf is set by ingest on a new ad whose item was seen before.
	RepostOf *Repost `json:"repost_of,omitempty"`
	// Scanned is set by a gem scan: store the ad, announce nothing, and
	// leave a new one unseen for the feeds.
	Scanned bool `json:"scanned,omitempty"`
}

// AdPageRead is what reading a stored ad's own page found, for ingest to
// keep: the listing with the page's facts added, or that the ad is gone.
type AdPageRead struct {
	Listing ListingDiscovered  `json:"listing"`        // what the page was read for
	Read    *ListingDiscovered `json:"read,omitempty"` // with the page's facts; nil when Gone
	Gone    bool               `json:"gone,omitempty"` // removed from the site
}

// GemScanRequested asks the scraper to scan one category of a source for
// the gem finder. Mode is "full" or "rate".
type GemScanRequested struct {
	Source   string `json:"source"`
	Category string `json:"category"`
	Mode     string `json:"mode"`
}

// Repost is what was known about a reposted ad's item before.
type Repost struct {
	ExternalID  string    `json:"external_id"`  // the latest earlier ad
	FirstListed time.Time `json:"first_listed"` // when the item was first seen, in any ad
	Times       int       `json:"times"`        // earlier ads of the item
	Price       string    `json:"price,omitempty"`
	PriceValue  *float64  `json:"price_value,omitempty"` // the latest earlier ad's
}

// Field is one labelled fact in a notification. Name is English; Names
// holds it in other alert languages, by code ("lv"), where the source has
// them. The value is shown as the site wrote it.
type Field struct {
	Name   string            `json:"name"`
	Names  map[string]string `json:"names,omitempty"`
	Value  string            `json:"value"`
	Inline bool              `json:"inline,omitempty"`
}

// NameIn is the field's name in lang, English when it has none there.
func (f Field) NameIn(lang string) string {
	if n := f.Names[lang]; n != "" {
		return n
	}
	return f.Name
}

// Languages alerts can be written in. A user who never chose reads
// DefaultLang; an alert with no Lang (sent before alerts had one) is
// English.
const (
	LangLatvian = "lv"
	LangEnglish = "en"
	DefaultLang = LangLatvian
)

// AlertLangs are the alert languages, the default first.
var AlertLangs = []string{LangLatvian, LangEnglish}

// Num returns a numeric attribute, as sources put numbers in Attrs and JSON
// round-trips them as float64.
func (l ListingDiscovered) Num(key string) (float64, bool) {
	v, ok := l.Attrs[key].(float64)
	return v, ok
}

// Str returns a string attribute.
func (l ListingDiscovered) Str(key string) (string, bool) {
	v, ok := l.Attrs[key].(string)
	return v, ok
}

// Channel is where a user wants to be notified.
type Channel struct {
	Kind   string `json:"kind"`   // "discord", later "telegram"
	Target string `json:"target"` // channel/chat ID on that platform
}

// NotificationRequested asks the notifier to deliver one message. Key is the
// idempotency key: delivering the same key twice must post once.
type NotificationRequested struct {
	Key        string             `json:"key"`
	UserID     int64              `json:"user_id"`
	SearchName string             `json:"search_name,omitempty"`
	Channel    Channel            `json:"channel"`
	Listing    *ListingDiscovered `json:"listing,omitempty"`
	Text       string             `json:"text,omitempty"` // used when Listing is nil
	// Lang is the language the user reads alerts in (see AlertLangs); the
	// notifier writes its own words in it. Text comes already written.
	// "" = English: a record from before alerts had a language.
	Lang string `json:"lang,omitempty"`
}
