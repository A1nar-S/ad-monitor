# Project notes (compacted from the planning conversation, 2026-10-28)

Read this first when picking the project back up. Future work and priorities live in
[ROADMAP.md](ROADMAP.md).

## Goal
One multipurpose offer-monitoring platform: discover new classified ads and track price,
discount, and availability changes on known offers. Examples: an Audi search, flats in Riga,
or waiting for a particular Apple Watch to reach a target price. Shared dashboard, users,
source adapters, storage, and notification delivery. Backend in **Go**.

Minimize maintenance overhead: evolve this repo and reuse the existing pipeline rather
than creating a separate application or bot for each website or watch type. Retail and
price-change monitoring are planned, not implemented; see [ROADMAP.md](ROADMAP.md).

## Starting point
The repo began as a docker-compose (Kafka KRaft + Postgres) with empty service folders; the
event-driven direction is the one we are building towards.

## Decisions
1. **Single Go module, multiple binaries** (`cmd/*`), not a module/repo per service. Atomic
   cross-service changes, one dependency set, `internal/` enforces boundaries. Services still
   deploy and scale independently.
2. **Vertical slice organisation** inside `internal/`: group by feature, not by layer. A slice
   owns its types, storage, migrations, logic and tests. Slices do not import each other's
   internals.
   - `internal/events`: the only shared package. Contracts crossing slices (become Kafka payloads).
   - `internal/scraping/<source>`: fetch and parse a site (`ss`, later `city24`, ...); `internal/sources` is the plugin interface.
   - `internal/listings`: persistence, upsert, new-listing detection. Owns the `listings` table.
   - `internal/notifications`: `Notifier` interface, idempotent `Service`, one subpackage per channel (`discord`, later `telegram`).
   - `internal/subscriptions`: users, channels, searches, matching.
   - `internal/platform`: config, db/migrations runner, later kafka/otel/logging helpers.
3. **Runtime architecture:** Kafka event bus with services
  `scraper -> ingest (outbox) -> matcher -> notifier`, plus `api` and a dashboard
  (Vue 3 SPA with Bun, no SSR). Future scheduler and infrastructure work is tracked in
  [ROADMAP.md](ROADMAP.md).
4. **Database: sqlc + goose (not GORM).** goose manages migrations, sqlc generates typed query
   code from hand-written SQL. GORM's AutoMigrate is not a real migration manager and a model
   per table fights the slice layout. Each slice owns `migrations/` (goose files), `queries/`,
   `sqlc.yaml` and generated code (`<slice>db/`); regenerate with `go generate ./...`. Each slice
   tracks versions in its own table (`<slice>_migrations`) via `platform/db.Migrate`.
5. **Suggested libs:** `franz-go`, `pgx`, `connect-go`/gRPC, `chi`,
   `goquery`, `discordgo`, `go-telegram/bot`, `otel`.

## Architecture (built)
```
scraper --listings.discovered--> ingest (upsert + outbox) --listings.new--> matcher --notifications.requested--> notifier
bot (Discord) ─┐
dashboard api ─┴─> subscriptions (users, channels, searches, logins) <── read live by scraper and matcher
```
- **Self-service, no config file.** People manage their own searches with the chat bot or the dashboard; there is no
  users file. Both go through one write path, `subscriptions.Service` (`CreateSearch`/`UpdateSearch`/`SaveSearchByName`,
  `SetEnabled`, `DeleteSearch`), which compiles the search through the source's `sources.Catalog` (validation, "did you
  mean") and the filter parser. A search is a **category** (`cars`, `flats`) plus form-style filters; users never see feeds.
  `searches.spec` keeps what the user wrote (for editing), `feeds`/`filters` what the pipeline runs on. The scraper and
  matcher read the table every cycle/ad, so changes apply without restarts. Per-user limit `MAX_SEARCHES_PER_USER`;
  `ADMIN_USERS` (`platform:id`) see and edit everyone's. Another user's search is "not found".
- **Identity:** a chat account, `users(platform, external_id)`; channels are `(user, kind=platform, target)` with a label.
  The channel kind is the notifier kind, so alerts for a Telegram user would go through a Telegram notifier.
- **Bot** (`internal/bot`): platform-neutral core (commands as specs, `Run`/`Complete`/`Act`, replies with neutral menus)
  behind a `Messenger` interface; `internal/bot/discordbot` is the Discord adapter (slash commands, autocomplete from the
  codebooks, select menus; gateway connection, so it works behind NAT). Depguard keeps discordgo out of the core. A
  Telegram adapter would implement `Messenger` and map the same specs to `/watch cars make=… price_max=…` and inline
  keyboards. Commands: `/watch cars|flats`, `/edit cars|flats` (merges the given filters into the search; `clear:`
  removes filters, bounds, whole ranges or the channel). Car searches take `keywords` (any of the words in the ad text:
  the list snippet first, the ad page's description only if needed; `Filters.Contains` on the `text` attr), `/searches`, `/matches`, `/unwatch`, `/dashboard`. Users can
  manage their own searches fully from the bot; the dashboard is optional. `channel:` is accepted only where the user may
  post (View + Send Messages, Manage Channels or Administrator): the adapter works it out from the gateway's cached roles
  and overrides and passes it as `Call.Postable`. `/setup` (status channel, stored in the `settings` table and read by
  the scraper per status message; `ADMIN_CHANNEL_ID` is the fallback) is shown to Manage Server members only and needs a
  bot admin: `ADMIN_USERS` plus the Discord app's owner or team, read at start. Alerts go by DM unless `channel:` is given; closed DMs get a warning, and the notifier drops "cannot DM"/"unknown channel" errors instead of retrying.
- **Dashboard login:** `/dashboard` replies with a one-time link (`PUBLIC_URL/api/login?token=…`, 10 minutes, single use)
  that becomes a 30-day HttpOnly SameSite=Lax session cookie. Only SHA-256 hashes of tokens are stored. Changes need an
  `X-Requested-With` header (CSRF). No OAuth app to set up.
- **Sources are plugins** (`internal/sources.Source`): `FetchFeed`, `PlanFeeds` (merge feeds to cut requests),
  `InScope` (does an ad belong to a search's feeds). `internal/events.ListingDiscovered` is source-neutral: common
  fields plus `Attrs` (filterable facts) and `Fields` (what to display). Register a new site in `internal/app/sources.go`.
  Searches filter generically on `Attrs` keys (`{min,max}` ranges, string sets, `price`), so cars/vinted need no core change.
- **ss.com** (`internal/scraping/ss`): a feed is a category path below `/lv/` (`transport/cars`,
  `real-estate/flats/riga/centre/hand_over`). The parser is **header-driven** (reads column meaning from the page's header
  row; unknown layout = error, not guesswork). New category = new entries in `columns`.
- **Codebooks** (`internal/scraping/ss/codebook/{cars,flats}.json`, embedded): ss.com's own search-form vocabulary
  (field IDs, option names, makes→models, regions→districts with URL slugs). Searches are written in these names and
  validated on save (`sources.Catalog.CompileSearch`: unknown names fail with "did you mean"). Parsed attrs use the
  same names, and ad pages are read through the same field IDs (`td#tdo_<id>`). `Catalog.Categories()` serves them to
  the bot's autocomplete and the dashboard's forms. A checked-in snapshot rather than a DB table: versioned, offline,
  reviewable diffs. Refresh with `go run ./cmd/admin ss-codebook` (~85 requests), then rebuild.
- **Cars = shared deal feeds.** The deal (Pārdod/Pērk/Maina) is the feed: `transport/cars/sell` (~1000 new ads/day,
  about 3.5 per 5 min), `/buy`, `/change`, each one request per cycle for all users. A search with no deal is stored as
  `transport/cars` and scraped as all three, never as the "Visi" page itself: that page cannot tell an ad's deal, and an
  ad is announced only on first sight, so it would hide ads from deal searches. Ads keep the feed they were first seen
  on, but a narrower one replaces it (older ads from `transport/cars` become `…/sell` when seen there).
  Filters on list columns are free (make, model, year, engine, fuel, mileage, price).
  Ad-page-only fields (gearbox, body, color, inspection, equipment; flats: house type) are read **lazily by the matcher**
  (`sources.Enricher`): only for a new ad that passes every cheap filter of some search (or is an alerted car), at most
  once per ad, shared by all searches. If the ad page fails, candidates get the alert flagged "⚠️ Unconfirmed" rather than nothing; a removed ad
  is announced to no one. ss.com's own server search was rejected: it is POST + session, 2 requests per search per cycle.
- **Reposts:** sources set `Fingerprint` (the item, not the ad; ss.com: `scraping/ss/reposts.go`). A new ad with a known
  fingerprint is still announced, as an orange "♻️ Reposted listing" with its price history.
- **Gem finder** (`internal/gems`, a tab for everyone; only admins start a fetch) is category-generic: a
  `gems.Category` (cars: `internal/gems/cars`, over `internal/deals`) says which stored ads belong, rates them
  (score, verdict, breakdown, candidates for an ad-page read) and declares a `Spec` (choice and range fields, sorts)
  that drives the API's filters and the dashboard's filters and cards; values live in `gems.attrs` (JSONB). A source
  joins by implementing `sources.Scannable` (`ScanScope(category)`: the feed of everything for sale, the location
  prefix of stored ones); a field with `Catalog` takes its options from the source's search catalog (cars: region).
  Adding flats = a `gems/flats` category + ss.com `ScanScope("flats")` (it has no all-flats feed yet). A scraper
  restart marks its running scans interrupted. Two passes. Pass 1 reads every page
  of `transport/cars/sell`, marks vanished cars sold, rates every car from its list row and saves at once. Pass 2 reads
  the ad page of every potential buy (`deals.Candidate`: price verdict Fair or better, not suspicious, no risky
  engine, mileage x ≥ −0.5) not read before (`listings.detailed_at` is the cache), saving every 100, then rates
  everything again. Pacing: the site's shared `scraping.Throttle` (REQUEST_GAP, jittered up to +50%) plus
  `GEM_DETAIL_GAP_SECONDS` (default 1, jittered) per ad page, a 20–40 s break every 100, and on 429/403/5xx pauses of
  1, 2, 4 min; the third push back (or 10 failures) in a row ends pass 2 with a note. ~8k candidates take ~8 h the
  first time, then only new ones. Body, region and gearbox (the filters) exist only for read cars; a car with
  too few similar ads has no price verdict, so it is never a candidate. A running scan blocks a new request until it
  shows no progress (`progress_at`) for 30 minutes. A scan's `mode` is `full` (the above) or `rate` ("Recalculate":
  pass 1 skipped, the stored listings rated again, then pass 2 for candidates not read yet; for a rating change or to
  finish a scan cut short). Filter counts are faceted: each choice counts the gems every other filter lets through
  (`GemFacets`; a choice also ignores the ones it narrows, a make the model). `db.Open` turns Postgres JIT off: it
  spent ~0.5 s compiling the facet query that runs in ~40 ms.
  Scanned-only rows have `seen_by_feed = false`, so alerts behave as if no scan ran.
- **Car verdicts** (`internal/deals`): overall, price vs similar stored ads, mileage for its age, engine/gearbox
  reputation and trim level (`deals/codebook/*.json`). The score is a weighted sum of parts x ∈ [−1, 1] (`score.go`):
  price 3 (z = gap in euros / `max(0.12·m, 0.2·√(15000·m))`, m the peers' median: 20% at 15 000 €,
  63% at 1 500 €, so a cheap car's few hundred euros don't sink it), mileage 2 (`mileage.go`: km vs 18 000/year, −log₂ ratio, weighted `10/(age+5)` so the same
  km costs a new car more), technical inspection 2 (ad page: none or expired −1, < 3 months left −0.25, valid 0),
  engine 2 (risky × wear `km/200k`), gearbox 1.5, model reliability 1.5, equipment 1.5. Good ≥ 4, bad ≤ −2,
  shown as `50 + 5·S` (0–100). Alerts and the gem finder share it. Texts come in `deals.Langs` (`lang.go`:
  English, the alerts'; Latvian for the gem finder, stored per gem in `gems.translations` and picked by `?lang=`;
  codebook entries need `notes_lv`). No emojis: the gem finder gets each verdict in parts (`deals.LinesIn`:
  tone, label, subject, text, has/lacks lists) and draws its own icons and colours. The vocabulary is English; sources translate via
  `CarAds.CarTerm` (ss.com: `scraping/ss/cars.go`).
  Car alerts read the ad page if no filter did already (one request; a failed read is tolerated).
- **Model reliability** (`deals/failrate.go`): how often the model fails inspection at the first attempt (±1 year, same
  fuel) vs all cars of known models that age, from CSDD's open data (data.gov.lv, CC0; per model, no VIN). Refresh yearly with
  `go run ./cmd/admin csdd-inspections` (streams ~700 MB, writes `deals/codebook/inspections.json`).
- **Flats:** `region`/`district`/`deal` compile into feed paths (shared by everyone watching the area); the rest are local
  filters.
- **rrr.lt** (`internal/scraping/rrr`, used car parts): read through the site's own GraphQL API (`POST /graphql`,
  `searchProducts`, newest first, 15 per page, offset cursors), not HTML. A feed is the canonical string of one search's
  server-side filters (`cat=230&model=1:1945&year=2018-&price=-150&quality=USED`); equal searches share it, and feeds
  are never merged, because the site adds thousands of parts a day. A part is new once, from whichever feed saw it first,
  so `InScope` checks other feeds' constraints against its attrs (category ancestors from the codebook, make/model ids,
  year, price, enums); keywords are the only local filter (title, part numbers, donor car). No ad-page reads: the API
  returns everything. Codebook `internal/scraping/rrr/codebook/parts.json` (part tree, makes→models) comes from one API
  request: `go run ./cmd/admin rrr-codebook`. A search needs a part or a make. Filters: part, make, model, car year,
  side, part number/words, price, condition. The donor car's body type (55-60% "other"), fuel (missing on 22-35%)
  and gearbox were dropped after checking the site's facet counts: they hid parts more than they found them. robots.txt disallows `/graphql` for
  crawlers; the load is a few paced queries per cycle, like one person browsing.
  Persisted source health and admin failure/recovery alerts are not implemented yet; they are
  a required foundation in [ROADMAP.md](ROADMAP.md#monitoring-required-for-scraping).
- **Scrape budget and paging:** feeds are derived from users' searches, de-duplicated, and >`FEED_COLLAPSE_AT` siblings
  merge into the parent feed. Feeds are ordered by last bump, not ID, so a known row at the *top* means nothing (it was
  bumped). The runner fetches the next page until the **last 2 rows** of a page are both known, up to `MAX_PAGES` (5). "Seen" is
  the listings table plus the previous cycle in memory, so a restart catches up on downtime. A feed's first-ever scrape
  is page 1 only (it is stored silently anyway). Hitting `MAX_PAGES` without reaching known rows sends one admin alert
  ("feed busier than N pages") and halves the wait until it catches up. Global request gap, jitter, backoff on
  403/429/5xx; with no feeds yet it re-checks every minute.
- **No spam on first start:** a feed's first scrape (`feed_state`, `SEED_WINDOW_SECONDS`) is stored silently, for any new
  feed, including one added by a new search later. Cost: a genuinely new ad inside that window is not announced.
  A new search on an already watched feed (all car searches) never back-announces: the matcher sees only new ads.
- **Flood guard** (`internal/notifications`): at most `NOTIFY_BURST` (5) ad messages per (user, search) per
  `NOTIFY_BURST_WINDOW_MINUTES` (10); the rest wait in `digest_queue` and go out as one digest per search when the window
  ends. Covers catch-up after downtime, broad searches and bugs alike. Operational texts are never limited.
- **Delivery:** at-least-once; dedup by DB keys (`listings` unique, `matches`, `deliveries` idempotency key).
  Matches are recorded after publishing, so a crash re-publishes rather than loses; the notifier dedups.
- **Dead letters:** `kafka.Consume` (takes a `kafka.Subscription`: brokers, group, topic) retries a failing handler 5
  times, then publishes the record (raw key and value, original headers) to `<topic>.dlq` (namespaced like the topic)
  and only then commits its offset. Undecodable records go straight there. Added headers: `dlq.original_topic`,
  `dlq.partition`, `dlq.offset`, `dlq.group`, `dlq.error` (cut to 1 KB), `dlq.failed_at` (RFC 3339), `dlq.reason`
  (`handler_failed` | `undecodable`). If the DLQ publish fails it is retried with backoff and the offset is not
  committed; on shutdown the record stays uncommitted and is redelivered (handlers are idempotent). Ingest, matcher
  and notifier all use it. `kafka-init` creates `<topic>.dlq` (1 partition, 30 days retention); with `NAMESPACE`
  set the namespaced DLQ topic is auto-created instead.
  - Inspect: `make dlq TOPIC=listings.new` (`go run ./cmd/admin dlq list [-limit N] <topic>`): offset, reason, error,
    failed-at, key, size; never the payload. Shows only letters not yet replayed.
  - Replay: `make dlq-replay TOPIC=listings.new` (`admin dlq replay <topic>`) republishes them to the original topic
    (same key and headers, `dlq.*` stripped) and commits in the group `<ns.>dlq-replay.<topic>`, so a second run does
    nothing. Replay is at-least-once: a crash between republishing and committing republishes that record on the next
    run (consumers are idempotent); run only one replay per topic at a time. Fix the cause first: a still-failing record is dead-lettered again. The admin container needs Kafka up.
  - Tests: unit tests in `internal/platform/kafka/dlq_test.go`; `TestDeadLetters` (integration) covers a poison and an
    undecodable record, no redelivery after restart, list, and a single replay.
- **Lifecycle:** `make purge` / `go run ./cmd/admin purge` deletes listings not *seen* for `RETENTION_HOURS` (default 60
  days). The listings table doubles as the "already announced" memory: a shorter retention re-announces old ads when they
  are bumped. Matches/deliveries are kept 2x longer. Cron it weekly.
- **Services:** `cmd/{scraper,ingest,matcher,notifier,bot,api,admin}` are thin; wiring lives in `internal/app` so tests
  run the real thing.
- **Dashboard:** `web/` is a Vue 3 + TypeScript SPA (Bun, Vite), built into every image and served by `cmd/api`
  (`internal/web`: API + SPA). Tabs: your searches (editor generated from `/api/categories`), your alerts, and for
  admins the old read-only overview (`internal/dashboard`'s cross-slice queries). Compose binds it to `127.0.0.1:8080`;
  publish it through a tunnel and set `PUBLIC_URL` for others. `cd web && bun run dev` proxies `/api` to a local API.

## Price watches (built 2026-11-10)
- **Any page, picked by pointing.** The dashboard loads a URL through the API (`POST /api/pages/inspect`),
  which keeps a copy for 30 minutes and serves it to a sandboxed iframe (`/api/pages/snapshots/{id}`:
  scripts stripped, opaque origin, strict CSP with a nonce for the picker only). `internal/scraping/pages/picker.js`
  draws the hovered element's margin/border/padding/content boxes like dev tools; a click posts a CSS selector
  back. Every change is read back by the server (`/api/pages/test`), so "the monitor reads" is what the watch
  will see. JSON responses show as a tree; a click picks a path (`data.items[0].price`).
- **Extractor** (`sources.Extractor`): CSS selector (first match), optional attribute, optional JSON path
  (JSON-LD scripts, APIs), fallback currency. Prices are `sources.Price` in exact cents. Parsing handles
  `1 299 €`, `1.299,00`, `$1,299.99`, `1299,-`; values from JSON/attributes must be plain amounts.
- **Streamed pages** (React/Next.js): content arrives in `<div hidden id="S:n">` and inline `$RC`/`$RS` calls
  move it into place. Both the copy and the reader replay those calls, so selectors match in both.
- **Fetching** only reaches public addresses (checked after DNS, on every redirect): users can't make the
  server read Postgres, Kafka or cloud metadata. Per-host request gap, 8 MB cap, browser-like UA, no
  workaround for 401/403/429.
- **Storage** (subscriptions slice, migration 0004): `watch_targets` (url + extractor, shared, last valid price,
  failure count, lease via `next_check_at`), `watches` (user, channel, rule, fixed reference, `below` flag),
  `watch_prices` (history, a row per change), `watch_alerts` (history and outbox).
- **Checking** runs inside the scraper service (`app.watchLoop`): every 30 s it leases due targets, reads each
  once for all its watches, and commits the reading, watch states and alerts in one transaction. The relay
  publishes alerts to `notifications.requested` with key `watch-alert:<id>`. "Check now" in the dashboard
  runs the same code in the API process (at most once per 30 s per page).
- **Limits:** `MAX_WATCHES_PER_USER` (10, admins exempt), `WATCH_MIN_INTERVAL_MINUTES` (30).
- **Tests:** `PAGES_LIVE=1 go test ./internal/scraping/pages -run TestLive` reads the Strandberg page the
  picker was designed on.

## Metrics
Every long-running service (`scraper`, `ingest`, `matcher`, `notifier`, `bot`, `api`) serves Prometheus metrics on
`METRICS_ADDR` (`internal/platform/metrics`; default `127.0.0.1:9100` so a host `go run` is not exposed, empty disables; a busy port only logs a warning). Go
runtime and process metrics come with it. Compose sets `METRICS_ADDR=:9100` (all interfaces of the container) for each service without publishing it, and
a `prometheus` service (profile `app`, `infra/prometheus/prometheus.yml`, volume `prometheus-data`, 15 days of
retention) scrapes `<service>:9100` every 15 s. Its UI: http://127.0.0.1:9090 (`make up` starts it; `Status > Targets`
shows what is scraped).

Every app metric is prefixed `admon_`. Labels are bounded (source, topic, kind, result, route pattern, status): never
URLs, user/search IDs, feed paths or error text. Metrics live in the package that owns the logic (`metrics.go` there).
The same code runs in several processes (e.g. `admon_kafka_*` in every service), so group by `job` when needed.

| Name | Type | Labels | Meaning |
|---|---|---|---|
| `admon_kafka_consumed_total` | counter | `topic`, `result` (ok, dead_lettered) | Records finished by `kafka.Consume`; `dead_lettered` = sent to `<topic>.dlq` (after retries, or undecodable) and committed |
| `admon_kafka_dead_lettered_total` | counter | `topic` (source topic), `reason` (handler_failed, undecodable) | Dead-lettered records by reason; any increase needs a look (`make dlq TOPIC=...`) |
| `admon_kafka_dlq_publish_total` | counter | `topic` (the `.dlq` topic), `result` (ok, error) | Attempts to publish a dead letter; errors are retried until they work, so a rising `error` with no `ok` means a stuck partition |
| `admon_kafka_handle_duration_seconds` | histogram | `topic` | One handler attempt |
| `admon_kafka_handle_retries_total` | counter | `topic` | Handler attempts repeated after an error |
| `admon_kafka_published_total` | counter | `topic`, `result` (ok, error) | Records published; `error` includes marshal failures |
| `admon_scraper_requests_total` | counter | `source`, `status_class` (1xx, 2xx, 3xx, 4xx, 5xx, other, error) | HTTP requests to the site (feed and ad pages, also in the matcher; not price-watch page reads) |
| `admon_scraper_feed_fetches_total` | counter | `source`, `result` (ok, empty, blocked, failed) | Feed page fetches; `empty` = page 1 returned 200 with no rows (layout change?); empty later pages are normal paging and count as ok |
| `admon_scraper_listings_discovered_total` | counter | `source` | Listings read and published |
| `admon_scraper_cycles_total` | counter | `source`, `result` (ok, idle, failed) | Scrape cycles; `idle` = no feeds to watch |
| `admon_scraper_cycle_duration_seconds` | histogram | `source` | Cycle duration |
| `admon_scraper_last_success_timestamp_seconds` | gauge | `source` | Unix time of the last cycle without errors (alert on `time() - x`) |
| `admon_ingest_listings_total` | counter | `result` (new, seeded, known) | Listings stored; `new` is announced, `seeded` is new but silent (feed seed window) |
| `admon_ingest_reposts_total` | counter | — | New ads announced as reposts |
| `admon_outbox_relayed_total` | counter | `result` (published, failed) | Outbox relay to `listings.new` |
| `admon_outbox_backlog` | gauge | none | Outbox entries not yet published (read from Postgres at scrape time; NaN if unreadable) |
| `admon_matcher_listings_total` | counter | `source`, `result` (matched, unmatched) | New listings fully handled by the matcher, counted once after recording (`unknown` source for unregistered ones) |
| `admon_matcher_notifications_total` | counter | `source` | Notification requests produced, counted once per listing after they are recorded (not per retry) |
| `admon_matcher_adpage_reads_total` | counter | `source`, `result` (ok, gone, failed) | Ad page reads (attempts: a Kafka retry reads again) |
| `admon_notifier_deliveries_total` | counter | `kind` (notifier kind, or `unknown`), `result` (sent, failed, dropped, digested) | Deliveries per notifier kind (digest messages count as sent); `failed` counts attempts, so one message retried 5 times adds 5 (unlike `admon_kafka_consumed_total`, which counts records) |
| `admon_watch_checks_total` | counter | `result` (ok, failed, blocked, gone, no_price) | Price watch page checks (scraper, and the API's "Check now") |
| `admon_watch_check_duration_seconds` | histogram | none | One check: page read plus recording |
| `admon_watch_alerts_total` | counter | `kind` (drop, failing, recovered) | Price alerts queued for delivery (counted after the transaction commits) |
| `admon_bot_commands_total` | counter | `command` (watch, edit, searches, matches, unwatch, dashboard, setup, menu_toggle, menu_delete, other), `result` (ok, rejected, error) | Bot commands and menu actions; `rejected` is the user's mistake |
| `admon_http_requests_total` | counter | `method`, `route`, `code` | API requests; `route` is the mux pattern (`/api/searches/{id}`, also `/api/` and the SPA catch-all `/`), `unmatched` if none; `method` is GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS or `other` |
| `admon_http_request_duration_seconds` | histogram | `method`, `route` | API request duration |

### Container and monitoring-stack metrics
A `cadvisor` service (profile `app`, `gcr.io/cadvisor/cadvisor` pinned in compose, no `ports:` entry, so only
Prometheus reaches `cadvisor:8080`) gives per-container CPU, memory, network and disk. Prometheus also scrapes
Grafana (`grafana:3000/metrics`, compose network only) is scraped too, and the existing `prometheus` self-scrape stays
as it was. Trust: cAdvisor mounts the Docker socket (read-only does not restrict API calls: it is root-equivalent on
the Docker host) and the host filesystem read-only; it is not published and the image is pinned. It runs with
`no-new-privileges`, all capabilities dropped and a 256 MB memory limit. Cardinality and CPU choices:
- cAdvisor flags: `--docker_only` (no systemd/host cgroups), 30 s housekeeping (default is 1 s) with dynamic
  housekeeping off, `--disable_root_cgroup_stats`, `--disable_metrics` for percpu, process, sched, tcp, udp, advtcp,
  hugetlb, referenced_memory, cpu_topology, cpuset, cpuLoad, memory_numa, resctrl, perf_event, pressure.
  `--store_container_labels=false` plus `--whitelisted_container_labels` keeps only `com.docker.compose.service` and
  `.project` (as `container_label_com_docker_compose_service` / `_project`); `name` and `image` are always there.
- `cadvisor` job scrapes every 30 s (cAdvisor refreshes only each housekeeping; faster scrapes repeat values).
  `metric_relabel_configs` keep only the families `containers.json` uses (CPU, working set, memory limit, network
  rx/tx bytes, fs reads/writes bytes, start time, scrape error), drop series without a `name`, and drop `id` (cgroup
  path with the container hash). Add a family to that `keep` regex when a panel needs it.
- Docker Desktop (Windows, WSL2, cgroup v2): verified that names and compose labels resolve with the standard
  read-only mounts and no `privileged`/devices. Only OOM-kill events are lost (no `/dev/kmsg`; the log warns once).
  Because a daemon-wide view is shown, containers of other compose projects on the same Docker show up too (filter by
  the `container_label_com_docker_compose_project` label, or by name).
- Memory limits: compose sets none, so `container_spec_memory_limit_bytes` is 0 and the limit line is simply absent
  (filtered with `> 0 < 1e15`, the upper bound covers cgroup v1's "unlimited"). "Without memory limit" counts them.
- There is no restart counter in cAdvisor: the "Restarts" stat counts `changes(container_start_time_seconds)` over the range, so it covers restarts of containers
  already present in the range (a container that first appears inside it shows 0). Of `/var/run` only `docker.sock` and `containerd/`
  are mounted: with the Docker socket alone, cAdvisor v0.55 fails to register its Docker factory (it also needs the
  containerd socket) and reports no containers.
- Rates in the dashboard use `$__rate_interval`; with a 30 s scrape it is at least 60 s, so two samples fit.

## Grafana
A `grafana` service (profile `app`, `grafana/grafana-oss` pinned in compose, volume `grafana-data`) is provisioned
entirely from files, so it is ready after `make up`: http://127.0.0.1:3000 (bound to this machine), user `admin`,
password `GRAFANA_ADMIN_PASSWORD` (required; `make env` generates it). Anonymous access, sign-up, analytics, update
checks and the news feed are off. The password is read as `${GRAFANA_ADMIN_PASSWORD:-}` and the container's
entrypoint wrapper exits with a message when it is empty (a `${VAR:?}` in the file would break every compose command
for a `.env` that predates it). Upgrade: add `GRAFANA_ADMIN_PASSWORD=<openssl rand -hex 16>` to the existing `.env`.
Stat panels that must not read as a healthy 0 when the series is missing (oldest scrape, outbox backlog) show a red
"No data" / "Unreadable"; counters the alarm stats use are created at 0 when `kafka.Consume` starts so `increase()`
sees the first event. Layout in `infra/grafana/`:
- `provisioning/datasources/prometheus.yml`: datasource uid `prometheus` -> `http://prometheus:9090` (dashboards use that uid).
- `provisioning/dashboards/dashboards.yml`: file provider reading `dashboards/` (mounted read-only, rescanned every 30 s).
- `dashboards/pipeline.json`, `price-watches.json`, `api-bot.json`: uids `admon-pipeline`, `admon-watches`, `admon-api-bot`.
- `dashboards/containers.json`: uid `admon-containers`. Rows: monitoring stack (scrape `up` of cAdvisor, Prometheus,
  Grafana, cAdvisor's own error flag), containers (variables `project`, default `infra` = the compose project, i.e. the
  `infra/` directory name, and `service` limited to it; CPU as % of one core, working set vs limit, network, disk,
  uptime, restarts) and Go services (variable `job`, the six app services only; `process_*`/`go_*` per scrape
  job: CPU, RSS, goroutines, heap in use, GC pause, open fds). Do not add `name:` to the compose file to pin the project
  name: it would rename the volumes and orphan the Postgres and Kafka data.
- `GF_PLUGINS_PREINSTALL_DISABLED=true` stops Grafana 11.x installing plugins (the default preinstalled list) on every
  start; the dashboards use built-in panels only. `GF_METRICS_ENABLED` serves `/metrics` for Prometheus.
- `dashboards/postgres-kafka.json`: uid `admon-postgres-kafka`, built on the exporters' `pg_*` / `kafka_*` metrics
  (see "Postgres and Kafka exporters").

The other dashboards use only the `admon_*` metrics above (plus `up`). Counters are shown with `increase(...[$__rate_interval])`
or `rate()`, latencies with `histogram_quantile` over `_bucket`. The "oldest successful scrape" stat goes red at 900 s,
i.e. 3x the default `CRAWL_INTERVAL_MINUTES=5`; edit it if the interval changes.

Changing a dashboard: the provider is read-only, so UI edits cannot be saved in place. Open the dashboard, edit and
tune it in the UI (Save is disabled, so use `Export > Export as JSON`, "export for sharing externally" off), replace the
matching file in `infra/grafana/dashboards/` and keep its `uid`; Grafana reloads it within 30 s. Edit the file directly
for small changes. Check expressions against Prometheus before committing.

## Postgres and Kafka exporters
Dashboard `postgres-kafka.json` (uid `admon-postgres-kafka`) reads two exporters, both profile `app`, pinned tags,
compose network only (no `ports:`), scraped by jobs `postgres` and `kafka` in `prometheus.yml`:
- **`postgres-exporter`** (`prometheuscommunity/postgres-exporter`) logs in as the role `admon_monitor` (LOGIN,
  `pg_monitor`, no superuser/createdb/createrole; `CONNECTION LIMIT 3`; cannot read table contents or create objects, though `pg_monitor` does show query texts in `pg_stat_activity`), not the owner account.
  The role is made by the one-shot `pg-monitor-init` (postgres image, `psql`, idempotent: create-if-missing, then
  `ALTER ROLE ... PASSWORD`, `GRANT pg_monitor`; the password is read with psql's `\getenv`, so it is not in argv, and statement logging is switched off for the session). It runs on every `up`, so fresh and existing databases need nothing
  by hand and a changed `MONITOR_DB_PASSWORD` is applied by the next `make up`. It connects as `DB_USER` (a superuser in
  this setup). Password: `MONITOR_DB_PASSWORD` (`make env` generates it). Compose reads it as `${MONITOR_DB_PASSWORD:-}`
  and the init container, if it is empty, prints a WARNING and exits 0 (no `${VAR:?}`: it would break compose for an older
  `.env`; a failing init would make `make up` exit non-zero). The exporter then cannot authenticate: it keeps running (no restart loop), `pg_up` is 0 (red on the dashboard) and it logs the auth error per scrape.
  `make up` also prints a note when `.env` lacks the key. Upgrade: add `MONITOR_DB_PASSWORD=<openssl rand -hex 16>` to `.env`
  (hex/alphanumerics only), then `make up`.
- **Prometheus config reload:** compose mounts `infra/prometheus/` (a directory; a single-file bind mount keeps the old
  inode after an editor replaces the file) and `make up` sends SIGHUP to Prometheus after `up -d`, so an edited
  `prometheus.yml` applies without recreating the container. Upgrade: the first `up` recreates `prometheus` for the new mount.
  Not enabled: `--collector.long_running_transactions` (v0.18.1 fails its scrape whenever no transaction is open);
  the longest transaction comes from `pg_stat_activity_max_tx_duration`.
- **`kafka-exporter`** (`danielqsj/kafka-exporter`) reads `kafka:29092`, all topics except `_*` (`--topic.filter`) and all
  groups, so a `NAMESPACE` prefix needs no setting; the dashboard matches DLQ topics by the `.dlq` suffix. Series used:
  `kafka_consumergroup_lag` (per group/topic/partition), `kafka_consumergroup_lag_sum`, `kafka_topic_partition_current_offset`
  and `..._oldest_offset`, `kafka_brokers`. Groups only appear after a first commit.
- **Cardinality:** `metric_relabel_configs` drop the ~250 `pg_settings_*` series except `max_connections`, `pg_statio_*`,
  `pg_locks_*`, per-table scan/tuple/vacuum counters (table sizes, `n_live_tup` and `n_dead_tup` stay), and Kafka
  member, replica and leader series.
- **Dead letters pending** (red when > 0) per `*.dlq` topic = lag of the `dlq-replay.<topic>` group (what `make
  dlq-replay` has not republished), or the records retained in the topic if that group does not exist yet. The topic
  keeps replayed records for 30 days, so "retained" alone would stay red after a replay; the dashboard's table shows both.
  With no broker data the stat shows red "No data", never a 0.
- The Pipeline dashboard also has a "Consumer lag" panel from `kafka_consumergroup_lag_sum`.

## Alerting
Grafana-managed alerting (not Prometheus rules), provisioned from `infra/grafana/provisioning/alerting/` (mounted
read-only, loaded at Grafana start; edit a file, then `docker compose ... restart grafana`; `make up` does not
recreate it for a changed provisioning file):
- `rules.yml`: folder `admon`, groups `availability` (1m), `pipeline` (1m), `services` (2m). Each rule: A = Prometheus
  instant query, B = reduce last, C = threshold (lag rule: A/B queries, C/D reductions, E math). Annotations
  `summary`, `description`, `__dashboardUid__`/`__panelId__` (file provisioning ignores top-level `dashboardUid`/
  `panelId`; the annotations are what give the Dashboard link), label `severity`. Annotation templates use a plain
  `$labels`/`$values` (no env expansion there).
- `contact-points.yml`: Discord `discord`, url `${GRAFANA_DISCORD_WEBHOOK_URL}`; `templates.yml`: `admon.title` and
  `admon.message` (severity, summary, description, job/service/topic/container/source, Dashboard and Silence links).
- `policies.yml`: root route to `discord`, group by `alertname, job, topic`, wait 30 s, interval 5 m, repeat 4 h;
  resolved messages on.
- **Webhook unset:** Grafana rejects a Discord contact point with an empty URL (provisioning error). The grafana
  entrypoint wrapper therefore exports an unreachable placeholder (`http://127.0.0.1:9/...`, connection refused at
  once) when `GRAFANA_DISCORD_WEBHOOK_URL` is empty and prints a notice; Grafana starts, rules evaluate and show
  in the UI, and each notification attempt fails with a logged error. Verified with and without the variable.
- Links use `GF_SERVER_ROOT_URL` = `GRAFANA_ROOT_URL` (default `http://127.0.0.1:3000`).
- **Error/no-data policy:** `execErrState: Error` everywhere (a Prometheus outage becomes `DatasourceError` alerts, grouped by alertname, with empty
  job/topic labels; not exercised yet); `noDataState: Alerting` where a missing series is itself the failure
  (`up`, Kafka, Postgres, monitoring, scraper stale), `OK` for event counters and rules whose cause another rule
  already covers.
- **Limits:** if Grafana is down nothing alerts (it runs the engine); Prometheus-side rules are not used.
- **Changing a rule:** edit `rules.yml` (not the UI: provisioned rules are read-only), restart Grafana, check
  Alerting > Alert rules. Test an expression in Prometheus first. Mute: `isPaused: true`, or a Silence in the UI.

| Rule (group) | Severity | Condition | for | noData / execErr |
|---|---|---|---|---|
| App service down (availability) | critical | `up{job=~"scraper\|ingest\|matcher\|notifier\|bot\|api"} < 1` | 2m | Alerting / Error |
| Kafka broker or exporter down | critical | `min(kafka_brokers or up*0) < 1` | 3m | Alerting / Error |
| Postgres down | critical | `min(pg_up or up*0) < 1` (also: missing `MONITOR_DB_PASSWORD`) | 5m | Alerting / Error |
| Monitoring component down | warning | `up{job=~"cadvisor\|postgres\|prometheus\|grafana"} < 1` | 5m | Alerting / Error |
| Scraper stale (pipeline) | critical | `time() - last_success > 900` s (3x default crawl interval) | 5m | Alerting / Error |
| Dead-letter publish errors | critical | `increase(dlq_publish{result="error"}[10m]) > 0` per topic | 2m | OK / Error |
| New dead letters | warning | `increase(dead_lettered[15m]) > 0` per topic, reason; description cites `make dlq TOPIC=...` | 0s | OK / Error |
| Consumer lag high | warning | `clamp_min(lag_sum,0) > 1000` and not smaller than 10 m ago | 10m | OK / Error |
| Outbox backlog high | warning | `sum(admon_outbox_backlog) > 200` | 10m | OK / Error |
| Notifier delivery failures | warning | `increase(deliveries{result="failed"}[15m]) > 5` per kind (5 = one message's retries) | 10m | OK / Error |
| Price watch checks failing (services) | warning | failed+blocked / all over 30 m `> 0.5`, only with >= 5 checks | 15m | OK / Error |
| API 5xx errors | warning | `increase(5xx[10m]) > 5` | 5m | OK / Error |
| Container restarting repeatedly | warning | `changes(container_start_time_seconds[30m]) >= 3` in any compose project | 5m | OK / Error |

Thresholds are for a small single-host setup (tiny traffic): counts rather than ratios where traffic is low, long
`for` where bursts are normal. Lag panels and the lag rule clamp kafka-exporter's `-1` (group without committed
offsets) with `clamp_min(..., 0)`. The Container rule covers any compose project; notifier `failed` and API 5xx counters are created on first use, so `increase()` misses the very first one (harmless at their `> 5` thresholds).

## Testing
- `go test ./...` unit tests; slice tests need `TEST_DATABASE_URL` (skipped otherwise). The bot core is tested with a
  fake `Messenger`, the Discord adapter's translation on its own, the web API with `httptest`.
- `go test -tags integration ./internal/app` runs scraper+ingest+matcher+notifier against real Postgres and Kafka
  (testcontainers, needs Docker) with a fake ss.com and fake Discord. It covers silent prefill, exactly-once
  notification, scraper restart, a new feed added while running, feed collapsing, and flats + cars. `TestCarsPipeline`
  covers the shared cars feed: ad pages read only for candidates, and a restart while 45 bump-ordered ads land on two
  pages (all announced exactly once, 5 singly and the rest in a digest). Set
  `TEST_DATABASE_URL` + `TEST_KAFKA_BROKERS` to use servers you already run.
- `SS_LIVE=1 go test ./internal/scraping/ss -run TestLiveStructure` compares the saved pages' column headers with the live
  site (a few real requests). Run it when the scraper starts failing, or now and then.
- `golangci-lint run ./...` and `golangci-lint fmt ./...` (config in `.golangci.yml`, includes depguard slice boundaries).
- golangci-lint must be built with Go ≥ 1.26 (older release images refuse the module); `go install
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` works.
- Postgres data now lives in the named volume `pg-data` (it used to be a root-owned bind mount under `infra/postgres`).
- Frontend checks: `cd web && bun run format:check && bun run build`. Apply formatting with
  `bun run format`; Prettier is pinned in the frontend development dependencies.
- GitHub Actions run all of the above on every push to `main` and every PR (`.github/workflows/ci.yml`: lint, tests
  with `-race` against a Postgres service, integration tests, `sqlc diff`, the dashboard build, the Docker image).
  `security.yml` adds govulncheck, `bun audit`, CodeQL (Go and TypeScript), gitleaks over the whole history (false
  positives go in `.gitleaksignore`) and dependency review on PRs, also weekly. Actions are pinned to commit SHAs;
  Dependabot updates them along with Go, Bun and Docker dependencies.

## Not verified
- Anything against a real Discord token: delivery, slash-command registration, autocomplete, select menus. The full
  Docker stack was run on 2026-11-08: dashboard login, search create/validate and the overview's admin check worked end to
  end; the bot and the notifier wait for a real `DISCORD_BOT_TOKEN`.
- Telegram does not exist yet: one more `Notifier` plus a bot adapter implementing `bot.Messenger`.

## Running it
See the README. In short:
```sh
make env                        # .env (random DB password); then the Discord values
make up                         # Postgres, Kafka, scraper, ingest, matcher, notifier, bot, dashboard (all in Docker)
make logs | make purge | make down  # make up also starts Prometheus: http://127.0.0.1:9090
make dlq TOPIC=listings.new     # records a consumer gave up on; make dlq-replay TOPIC=... republishes them
# Services on the host instead: make infra, then go run ./cmd/<service>; the dashboard with cd web && bun run dev
go run ./cmd/admin ss-codebook  # refresh the ss.com codebooks
go run ./cmd/admin rrr-codebook # refresh the rrr.lt part/model taxonomy (one API request)
go run ./cmd/admin csdd-inspections # refresh the CSDD model fail-rate table (yearly)
go generate ./...               # after changing a slice's migrations/ or queries/
```
Deploying: k3s + Cloudflare Tunnel (`deploy/`). Gotchas:
- `enableServiceLinks: false` everywhere: cp-kafka reads the injected `KAFKA_PORT` as broker config.
- GitOps: a `v*` tag makes the Release workflow push images to GHCR and commit their digests to `deploy/k8s/kustomization.yaml`
  (`images:`); Flux (`deploy/flux`) applies main. Before the first release the list maps to `:local` imports.
- Jobs carry `kustomize.toolkit.fluxcd.io/force: enabled` (immutable spec), no TTL (Flux would rerun them every sync);
  namespaces carry `prune: disabled`.
  Alert rule changes restart Grafana: Job `grafana-restart` mounts a content-hashed copy of the rules ConfigMap. The monitoring stack is its own Kustomization: PodMonitors need its CRDs first.
- `PUBLIC_URL` stays in `.env` (Secret `admon-env`): the public repo never names the domain.
- Init containers wait for Postgres and the topics Job; they run as numeric uids because pods are `runAsNonRoot`.
- Monitoring: kube-prometheus-stack via k3s's HelmChart (`deploy/k8s/monitoring`), no Alertmanager or default rules.
  PodMonitors keep the compose job names (`jobLabel: app`, relabel to `postgres`/`kafka`), so `infra/grafana`
  dashboards and rules load unchanged as ConfigMaps (sidecars). Two rules `or` in a k3s variant: monitoring down
  (`up{namespace="monitoring"}`) and restarts (`kube_pod_container_status_restarts_total`).
- Tested 2026-11-21 in a k3s container; bot, notifier and a real tunnel not yet.

Note: `go.mod` is on Go 1.26 (sqlc v1.31 requires it); an older local Go auto-downloads the toolchain.
Kafka's data is a named volume (`kafka-data`): the image runs as uid 1000 and a bind mount is created root-owned.
