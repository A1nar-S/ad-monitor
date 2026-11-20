# Offer monitoring roadmap

Planned work. Current architecture and setup: [PROJECT_NOTES.md](PROJECT_NOTES.md).

## Direction

One platform to discover offers and track prices across classifieds, marketplaces, and shops.
Support both search-based discovery and pinned-product watches.
Share collection work across users; keep their rules and notification channels separate.
Use the existing repo, dashboard, database, and bot. Keep maintenance low.

No paid data APIs. Prefer permitted free APIs/feeds, then rate-limited scraping.
Do not bypass access controls.

## Next steps

- [ ] Verify real Discord delivery and the bot's commands; add Telegram (a `bot.Messenger` adapter plus a `Notifier`).
- [x] Notification burst limits/digests (per search; `NOTIFY_BURST`).
- [ ] Schedule stale-data cleanup (`make purge` exists; needs a cron or a scheduler).
- [x] Self-service searches: Discord bot (`/watch`, `/edit`, `/searches`, `/matches`, `/dashboard`) and dashboard editing, with login links,
  per-user limits and admins. No config file.
- [x] ss.com codebooks for cars and flats; searches use the search forms' names, validated on seed.
- [ ] Serve the codebooks through the API for a dashboard search editor (then move them to a table).
- [ ] Replace category-path parsing with strongly typed categories across adapters,
  events, storage, API, and TypeScript; backfill existing records and handle unknown categories.

## Discovery at a target price

Find matching offers across configured sources without pinning a URL. The existing matcher
supports price ceilings; marketplace and shop discovery still need source adapters.

- [ ] Expose target-price searches for classifieds, marketplace searches, and shop catalogs:
  e.g. an Audi below EUR 10,000 or a specific Apple Watch below EUR 250 from supported sellers.
- [ ] Keep new matching-listing alerts separate from existing offers dropping below the
  target; the latter needs the price-change tracking below. Deduplicate both alert types.

## Price watches: first milestone (built)

Examples: an Apple Watch below EUR 250, or protein powder dropping 5% from its price when
the watch starts. Either rule, not just target prices.

- [x] Any product page or JSON API instead of one retailer: paste the URL, click the price in
  the dashboard's element picker (dev-tools-style box overlay on the server's copy of the page)
  or the value in a JSON tree. Structured data, shop meta tags and price-named form fields are
  suggested. React/Next.js streamed pages are revealed the way their scripts would.
- [x] Per-user watches: the element, target price or percentage drop, currency, interval
  (30 min to daily, `WATCH_MIN_INTERVAL_MINUTES`), and destination (DM or a channel).
- [x] Percentage rules keep the price at watch start as a fixed reference until reset in the
  dashboard: EUR 100 with a 5% rule alerts at EUR 95 or less, gradual drops included.
- [x] Pinned pages are read on their own schedule, shared: one read per page and element
  for everyone watching it.
- [x] Timestamped price history in exact cents with explicit currency (a row per change).
- [x] Readings, watch state and alerts commit together; alerts relay from an outbox, keyed per alert.
- [x] Silent baseline; alert on crossing at or below the threshold; re-arm above it.
- [x] Never a missing price as zero: failed reads keep the last price, a price under half the
  last one is read twice, a changed currency is a failure. Watchers get one "can't read"
  alert after 3 failures in a row and one when it reads again.
- [x] Tests: both rules, gradual drops, resets, retries, misreads, failures, shared watches,
  the outbox, the picker API, streamed pages, private-address refusal.

Next:
- [ ] Verify a real alert through Discord (the notifier path is the one searches use).
- [ ] Pages whose price only exists after JavaScript runs (an optional headless-browser reader).
  Today the picker shows such pages without the price and points to suggestions or the API.
- [x] Shops that refuse automated reads (401/403/429, Amazon often does) are reported as refused, not worked around.
- [ ] Admin incidents for failing pages in `#monitoring-alerts` (see Monitoring).

## Monitoring: required for scraping

- [ ] Track last valid success, failures, and stale/paused state per source and target.
- [ ] Detect broken layouts even on HTTP 200; distinguish parser failures, access issues,
  legitimate empty results, and unavailable products. Keep bounded, redacted diagnostics.
- [ ] Retain the last valid data on failure. Never interpret a missing price as zero or
  a broken parser as a discount/removal; validate suspicious changes before user alerts.
- [ ] Send deduplicated admin incidents and recovery alerts to `#monitoring-alerts`.
  Include affected watches, evidence, and a dashboard link; support mute windows and backoff.
- [ ] Flag abnormal result counts, missing fields, price/identity shifts, mass unavailability,
  request/error spikes, stuck queues, notification bursts, and storage growth.
- [ ] Start with configurable thresholds; add per-target rolling baselines after enough data.
  Allow normal fluctuations and real discounts; avoid alert storms and recovery flapping.
- [ ] Show incidents and source health in the dashboard. Add an independent watchdog for
  stopped workers/bot outages, and retry failed admin notifications.
- [ ] Test layout changes, anomalies, false positives, deduplication, and recovery.

## Later

- [x] Price history and last-check status in the dashboard.
- [ ] Price-watch commands in the bot (list, pause); creating one needs the picker.
- [ ] Restock rules. Keep advertised discounts separate from observed price drops.
- [ ] Preserve active watches and required price history during cleanup.
- [ ] Add City24, Vinted, and shop/marketplace adapters one at a time, with parser fixtures and health checks.
- [ ] Cross-store comparison: match variant, condition, seller, currency, and shipping/fees first.
  Start with manual product grouping.
- [x] CI: lint, tests, integration tests, sqlc, dashboard and image builds, security scans.
- [x] Prometheus metrics for every service (`admon_*`, see PROJECT_NOTES).
- [x] Grafana dashboards: pipeline, price watches, API & bot (`127.0.0.1:3000`).
- [x] Container, Postgres and Kafka monitoring: cAdvisor, postgres-exporter, kafka-exporter (consumer lag, dead letters).
- [x] Dead-letter queues for Kafka records that fail or cannot be decoded (`make dlq`, `make dlq-replay`).
- [ ] Protobuf, scheduler, and deployment tooling as needed.