// Package app holds the wiring of each service: what cmd/<name> runs. It lives
// here, not in main, so integration tests can start the real services
// in-process against real Postgres and Kafka with fake sites and notifiers.
//
// Every table has one writing service, which alone migrates it:
//
//	ingest    listings (also what other services read on ad pages)
//	api       users, searches, settings, price watches (dashboard and chat bot)
//	matcher   matches
//	notifier  deliveries and digests
//	scraper   gem scans
//
// The others read those tables (after AwaitSchema) and send what to write
// as events to the owner.
package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"log/slog"

	"github.com/A1nar-S/advertisiment-monitoring/internal/bot"
	"github.com/A1nar-S/advertisiment-monitoring/internal/bot/discordbot"
	"github.com/A1nar-S/advertisiment-monitoring/internal/bot/telegrambot"
	"github.com/A1nar-S/advertisiment-monitoring/internal/dashboard"
	"github.com/A1nar-S/advertisiment-monitoring/internal/events"
	"github.com/A1nar-S/advertisiment-monitoring/internal/gems"
	"github.com/A1nar-S/advertisiment-monitoring/internal/listings"
	"github.com/A1nar-S/advertisiment-monitoring/internal/matching"
	"github.com/A1nar-S/advertisiment-monitoring/internal/notifications"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/config"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/db"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/kafka"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/metrics"
	"github.com/A1nar-S/advertisiment-monitoring/internal/platform/telegram"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping"
	"github.com/A1nar-S/advertisiment-monitoring/internal/scraping/pages"
	"github.com/A1nar-S/advertisiment-monitoring/internal/sources"
	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions"
	"github.com/A1nar-S/advertisiment-monitoring/internal/web"
)

const relayBatch = 100

// digestCheckEvery is how often the notifier looks for digests that are due.
const digestCheckEvery = 30 * time.Second

// migrate runs, in order, the migrations of the slices a service owns and
// then waits for the schemas of those it only reads. Owners go first, so
// two services waiting on each other's schema never deadlock.
func migrate(ctx context.Context, own []func(context.Context) error, read ...func(context.Context) error) error {
	for _, m := range slices.Concat(own, read) {
		if err := m(ctx); err != nil {
			return err
		}
	}
	return nil
}

// publisher publishes events to the namespaced topics.
type publisher struct {
	p   *kafka.Producer
	cfg config.Common
}

// listing hands ingest an ad a scrape or a gem scan saw.
func (pb publisher) listing(ctx context.Context, l events.ListingDiscovered) error {
	return pb.p.Publish(ctx, pb.cfg.Topic(events.TopicListingsDiscovered), l.Source+"/"+l.ExternalID, l)
}

// adPage hands ingest what an ad page showed, for an ad it already stored
// (an unknown one is ignored). Keyed by the ad, so reads of one ad stay in
// order.
func (pb publisher) adPage(ctx context.Context, r events.AdPageRead) error {
	return pb.p.Publish(ctx, pb.cfg.Topic(events.TopicListingDetails), r.Listing.Source+"/"+r.Listing.ExternalID, r)
}

// notify asks the notifier to deliver; one user's messages stay in order.
func (pb publisher) notify(ctx context.Context, n events.NotificationRequested) error {
	return pb.p.Publish(ctx, pb.cfg.Topic(events.TopicNotificationsRequested), strconv.FormatInt(n.UserID, 10), n)
}

// Scraper fetches the feeds users watch, for every registered source, and
// publishes each ad it sees. It does no deduplication; ingest does. It also
// runs the gem scans admins request, and owns their tables.
func Scraper(ctx context.Context, cfg config.Scraper, reg sources.Registry) error {
	metrics.Serve(ctx, cfg.MetricsAddr)
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// The scraper reads which feeds searches want and which ads are stored
	// (to know where it left off); the api and ingest write those.
	subs := subscriptions.NewService(pool, reg, cfg.FeedCollapseAt)
	history := listings.NewService(pool, listings.DefaultSeedWindow)
	gemScans := gems.NewService(pool, gemCategories()...)
	if err := migrate(ctx, []func(context.Context) error{gemScans.Migrate}, subs.AwaitSchema, history.AwaitSchema); err != nil {
		return err
	}

	producer, err := kafka.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	pub := publisher{producer, cfg.Common}

	// Status messages go where /setup status-channel said, else to the .env
	// channel. Looked up per message: they are rare and the setting may change.
	alert := func(ctx context.Context, text string) {
		ch := events.Channel{Kind: cfg.AdminChannel.Kind, Target: cfg.AdminChannel.Target}
		if set, ok, err := subs.StatusChannel(ctx); err != nil {
			slog.Warn("status channel setting", "err", err)
		} else if ok {
			ch = set
		}
		if ch.Target == "" {
			return
		}
		err := producer.Publish(ctx, cfg.Topic(events.TopicNotificationsRequested), "admin", events.NotificationRequested{
			Channel: ch,
			Text:    text,
		})
		if err != nil {
			slog.Error("alert", "err", err)
		}
	}

	slog.Info("starting scraper", "sources", reg.Names(), "interval", cfg.CrawlInterval, "max_pages", cfg.MaxPages)
	alert(ctx, fmt.Sprintf("🚀 **Scraper started**\nSources: %v, every %s", reg.Names(), cfg.CrawlInterval))

	var wg sync.WaitGroup
	// One request spacing per site, shared by its scraping and gem scans,
	// jittered so the site never sees a fixed beat.
	throttles := map[string]*scraping.Throttle{}
	for _, name := range reg.Names() {
		throttles[name] = &scraping.Throttle{Gap: cfg.RequestGap, Jitter: requestJitter}
	}
	for _, name := range reg.Names() {
		runner := &scraping.Runner{
			Source:   reg.Lookup(name),
			Feeds:    subs,
			History:  history,
			Publish:  pub.listing,
			Alert:    alert,
			Interval: cfg.CrawlInterval,
			Gap:      cfg.RequestGap,
			MaxPages: cfg.MaxPages,
			Throttle: throttles[name],
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner.Run(ctx)
		}()
	}
	runGemScans(ctx, gemScans, history, pub, reg, throttles, cfg.GemDetailGap, cfg.GemRerateEvery, &wg)
	// Scan requests from the api: recorded here, where the scans run.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := kafka.Consume(ctx, subscription(cfg.Common, "scraper", events.TopicGemScansRequested),
			func(ctx context.Context, r events.GemScanRequested) error {
				_, err := gemScans.RequestScan(ctx, r.Source, r.Category, gems.Mode(r.Mode))
				switch {
				case errors.Is(err, gems.ErrBusy):
					return nil // asked twice; one scan does
				case errors.Is(err, gems.ErrBadQuery):
					return kafka.Permanent(err)
				}
				return err
			})
		if err != nil {
			slog.Error("gem scan requests", "err", err)
		}
	}()
	wg.Wait()

	slog.Info("stopping scraper")
	// ctx is cancelled; use a fresh one so the goodbye message can go out.
	bye, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	alert(bye, "⏹️ **Scraper stopped**")
	return nil
}

const (
	watchCheckEvery = 30 * time.Second
	watchRelayEvery = 2 * time.Second
	watchBatch      = 10
)

// watchLoop reads the price-watch pages that are due and relays the alerts
// they raise (also those raised by the dashboard's "Check now").
func watchLoop(ctx context.Context, subs *subscriptions.Service, reader sources.PageReader,
	publish func(context.Context, events.NotificationRequested) error) {
	check := time.NewTicker(watchCheckEvery)
	defer check.Stop()
	relay := time.NewTicker(watchRelayEvery)
	defer relay.Stop()
	checkDue := func() {
		for ctx.Err() == nil {
			n, err := subs.CheckDueWatches(ctx, reader, watchBatch)
			if err != nil && ctx.Err() == nil {
				slog.Error("check price watches", "err", err)
			}
			if n > 0 {
				slog.Info("price watches read", "pages", n)
			}
			if err != nil || n < watchBatch {
				return
			}
		}
	}
	checkDue()
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			checkDue()
		case <-relay.C:
		}
		for ctx.Err() == nil {
			n, err := subs.RelayWatchAlerts(ctx, relayBatch, publish)
			if err != nil && ctx.Err() == nil {
				slog.Error("relay price alerts", "err", err)
			}
			if err != nil || n < relayBatch {
				break
			}
		}
	}
}

// Ingest stores discovered listings and announces the new ones. It owns the
// listings tables: it also keeps what other services read on ad pages, and
// relays its outbox to Kafka.
func Ingest(ctx context.Context, cfg config.Ingest) error {
	metrics.Serve(ctx, cfg.MetricsAddr)
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := listings.NewService(pool, cfg.SeedWindow)
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	producer, err := kafka.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	metrics.SetGaugeFunc("admon_outbox_backlog", "Listings outbox entries not yet published to Kafka.", func() float64 {
		n, err := store.OutboxBacklog(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("outbox backlog metric", "err", err)
			}
			return math.NaN()
		}
		return float64(n)
	})

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		relayLoop(ctx, store, producer, cfg.Topic(events.TopicListingsNew))
	}()

	slog.Info("starting ingest")
	err = runAll(ctx, []func(context.Context) error{
		func(ctx context.Context) error {
			return kafka.Consume(ctx, subscription(cfg.Common, "ingest", events.TopicListingsDiscovered),
				func(ctx context.Context, l events.ListingDiscovered) error {
					_, err := store.Ingest(ctx, l)
					return err
				})
		},
		func(ctx context.Context) error {
			return kafka.Consume(ctx, subscription(cfg.Common, "ingest-details", events.TopicListingDetails),
				func(ctx context.Context, r events.AdPageRead) error {
					err := store.KeepAdPage(ctx, r)
					if errors.Is(err, listings.ErrNoRead) {
						return kafka.Permanent(err)
					}
					return err
				})
		},
	})
	stop()
	wg.Wait()
	return err
}

// relayLoop publishes outbox entries as they appear. Polling is plenty at this
// volume and avoids LISTEN/NOTIFY plumbing.
func relayLoop(ctx context.Context, store *listings.Service, p *kafka.Producer, topic string) {
	publish := func(ctx context.Context, l events.ListingDiscovered) error {
		return p.Publish(ctx, topic, l.Source+"/"+l.ExternalID, l)
	}
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for {
			n, err := store.RelayOutbox(ctx, relayBatch, publish)
			if err != nil && ctx.Err() == nil {
				slog.Error("relay outbox", "err", err)
			}
			if err != nil || n < relayBatch {
				break
			}
		}
	}
}

// Matcher decides which users care about each new listing and asks the
// notifier to tell them. It owns the matches; it reads the searches (the
// api's) and the stored ads (ingest's), to which it sends what the ad pages
// it reads show.
func Matcher(ctx context.Context, cfg config.Matcher, reg sources.Registry) error {
	metrics.Serve(ctx, cfg.MetricsAddr)
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	subs := subscriptions.NewService(pool, reg, cfg.FeedCollapseAt)
	store := listings.NewService(pool, listings.DefaultSeedWindow) // the stored ads price car alerts
	match := matching.NewService(pool, subs, reg)
	if err := migrate(ctx, []func(context.Context) error{match.Migrate}, subs.AwaitSchema, store.AwaitSchema); err != nil {
		return err
	}

	producer, err := kafka.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	pub := publisher{producer, cfg.Common}
	// Ad pages the matcher reads are kept, so the dashboard can show a car's
	// region, gearbox, ... without reading the page again. Failing to send
	// them is logged, not fatal: the alert matters more.
	match.WithAdPageHook(func(ctx context.Context, r events.AdPageRead) {
		if err := pub.adPage(ctx, r); err != nil {
			slog.Warn("could not keep ad page details", "listing", r.Listing.ExternalID, "err", err)
		}
	}).WithCarRating(carPeers{store})

	slog.Info("starting matcher")
	return kafka.Consume(ctx, subscription(cfg.Common, "matcher", events.TopicListingsNew),
		func(ctx context.Context, l events.ListingDiscovered) error { return match.Handle(ctx, l, pub.notify) })
}

// carPeers lets the matching slice price cars against the stored ads
// without importing the listings slice.
type carPeers struct{ store *listings.Service }

func (c carPeers) CarPeers(ctx context.Context, q matching.PeerQuery) ([]matching.Peer, error) {
	rows, err := c.store.CarPeers(ctx, listings.PeerQuery(q))
	if err != nil {
		return nil, err
	}
	out := make([]matching.Peer, len(rows))
	for i, r := range rows {
		out[i] = matching.Peer(r)
	}
	return out, nil
}

// subscription is what a consumer named name reads: the namespaced topic, in
// the namespaced group. Failed records go to that topic's dead-letter topic;
// those that failed because the database was unreachable are retried until
// it is back.
func subscription(c config.Common, name, topic string) kafka.Subscription {
	return kafka.Subscription{Brokers: c.KafkaBrokers, Group: c.Group(name), Topic: c.Topic(topic), Transient: db.Transient}
}

// DLQReplayGroup is the consumer group that remembers which dead letters of
// topic (a logical name such as events.TopicListingsNew) were replayed.
func DLQReplayGroup(c config.Common, topic string) string { return c.Group("dlq-replay." + topic) }

// Notifier delivers notification requests through the given channels.
func Notifier(ctx context.Context, cfg config.Notifier, notifiers ...notifications.Notifier) error {
	metrics.Serve(ctx, cfg.MetricsAddr)
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := notifications.NewService(pool, cfg.Delay, notifiers...).WithBurst(cfg.Burst, cfg.BurstWindow)
	if err := svc.Migrate(ctx); err != nil {
		return err
	}
	if cfg.Burst > 0 {
		go svc.RunDigests(ctx, min(digestCheckEvery, max(cfg.BurstWindow/2, time.Second)))
	}

	slog.Info("starting notifier", "burst", cfg.Burst, "burst_window", cfg.BurstWindow)
	return kafka.Consume(ctx, subscription(cfg.Common, "notifier", events.TopicNotificationsRequested), svc.Handle)
}

// Backoff between attempts to serve a chat platform again (vars: tests
// shorten them).
var (
	chatRetryFirst = 5 * time.Second
	chatRetryMax   = 5 * time.Minute
)

// keepServing runs serve until ctx is done, starting it again (after a
// growing pause) whenever it fails: a chat platform being down or a revoked
// token must not stop the dashboard and price watches beside it.
func keepServing(platform string, serve func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		wait := chatRetryFirst
		for {
			err := serve(ctx)
			if ctx.Err() != nil {
				return nil
			}
			if err == nil {
				err = errors.New("stopped")
			}
			slog.Error("chat bot down, retrying", "platform", platform, "in", wait, "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			wait = min(2*wait, chatRetryMax)
		}
	}
}

// runAll runs fns until ctx is done; the first to fail stops the others.
func runAll(ctx context.Context, fns []func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func() { errs <- fn(ctx) }()
	}
	var first error
	for range fns {
		if err := <-errs; err != nil && first == nil {
			first = err
			cancel()
		}
	}
	return first
}

// botAds shows the bot stored ads without the bot importing the listings slice.
type botAds struct{ store *listings.Service }

func (a botAds) Ads(ctx context.Context, source string, ids []string) (map[string]bot.Ad, error) {
	found, err := a.store.Summaries(ctx, source, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bot.Ad, len(found))
	for id, s := range found {
		out[id] = bot.Ad{Title: s.Title, URL: s.URL, Price: s.Price, Location: s.Location}
	}
	return out, nil
}

// API is the service people talk to: the dashboard and its JSON API, and
// the chat bot on every platform with a token (Discord, Telegram). Both
// manage the same users and searches, which this service alone writes; so
// are the price watches it checks on their own schedule.
func API(ctx context.Context, cfg config.API, reg sources.Registry) error {
	metrics.Serve(ctx, cfg.MetricsAddr)
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	admins := slices.Clone(cfg.Admins)
	var discord *discordbot.Adapter
	if cfg.DiscordBotToken != "" {
		if discord, err = discordbot.New(cfg.DiscordBotToken); err != nil {
			return err
		}
		// The Discord app's owners are admins too, so /setup works without
		// ADMIN_USERS.
		if owners, err := discord.Owners(); err != nil {
			slog.Warn("cannot read the Discord app's owners; only ADMIN_USERS are admins", "err", err)
		} else {
			for _, id := range owners {
				admins = append(admins, discordbot.Platform+":"+id)
			}
		}
	}

	subs := subscriptions.NewService(pool, reg, cfg.FeedCollapseAt).WithLimits(cfg.MaxSearches, admins).
		WithWatchLimits(cfg.MaxWatches, cfg.MinWatchInterval).WithPublicURL(cfg.PublicURL)
	store := listings.NewService(pool, listings.DefaultSeedWindow)
	gemList := gems.NewService(pool, gemCategories()...)
	match := matching.NewService(pool, subs, reg)
	subs.WithMatchLog(match)
	if err := migrate(ctx, []func(context.Context) error{subs.Migrate},
		store.AwaitSchema, gemList.AwaitSchema, match.AwaitSchema); err != nil {
		return err
	}

	producer, err := kafka.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()
	pub := publisher{producer, cfg.Common}

	webDir := cfg.WebDir
	if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		slog.Warn("no built dashboard; serving the API only (run bun run build in web/)", "dir", webDir)
		webDir = ""
	}
	server := &http.Server{
		Addr: cfg.Addr,
		Handler: metrics.HTTP(web.New(web.Config{
			Searches: subs, Listings: store, KeepAdPage: pub.adPage,
			Overview: dashboard.New(pool, reg),
			Sources:  reg, WebDir: webDir, PublicURL: cfg.PublicURL, MaxSearches: cfg.MaxSearches,
			// Pages for the price picker, read on the user's behalf; the
			// timeout fits the server's write timeout.
			Watches: subs, Pages: pages.New(10*time.Second, cfg.RequestGap), MaxWatches: cfg.MaxWatches,
			Gems: gemList, GemSources: gemSources(reg, gemList),
			RequestGemScan: func(ctx context.Context, r events.GemScanRequested) error {
				return producer.Publish(ctx, cfg.Topic(events.TopicGemScansRequested), r.Source+"/"+r.Category, r)
			},
		})),
		ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute,
	}

	serve := []func(context.Context) error{
		func(ctx context.Context) error {
			go func() {
				<-ctx.Done()
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(shutdown); err != nil {
					slog.Error("shutdown", "err", err)
				}
			}()
			slog.Info("dashboard", "listen", cfg.Addr, "public_url", cfg.PublicURL)
			if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
		// Price watches: pinned pages, read on their own schedule.
		func(ctx context.Context) error {
			watchLoop(ctx, subs, pages.New(cfg.RequestTimeout, cfg.RequestGap), pub.notify)
			return nil
		},
	}
	if discord != nil {
		core := bot.New(discord, subs, botAds{store}, cfg.PublicURL, cfg.MaxSearches)
		serve = append(serve, keepServing("discord", func(ctx context.Context) error { return discord.Serve(ctx, core, cfg.GuildID) }))
	}
	if cfg.TelegramBotToken != "" {
		tg := telegrambot.New(telegram.New(cfg.TelegramBotToken))
		core := bot.New(tg, subs, botAds{store}, cfg.PublicURL, cfg.MaxSearches)
		serve = append(serve, keepServing("telegram", func(ctx context.Context) error { return tg.Serve(ctx, core) }))
	}
	slog.Info("starting api", "chat_platforms", len(serve)-2, "max_searches", cfg.MaxSearches, "admins", len(admins))
	return runAll(ctx, serve)
}
