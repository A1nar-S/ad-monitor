# Advertisement monitoring

Watches ss.com (cars, flats) and any product page you pin, and sends matching new ads and
price drops to Discord.
Details: [docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md). Plans: [docs/ROADMAP.md](docs/ROADMAP.md).

## Run

Needs Docker, `make`, `openssl`.

```sh
make env    # creates .env; then set DISCORD_BOT_TOKEN
make up     # start everything
make logs
```

`make down` stops it. Messages a service could not process are kept in dead-letter topics:
`make dlq TOPIC=listings.new` lists them, `make dlq-replay TOPIC=listings.new` sends them through again (at-least-once: after a crash a record may be sent twice;
run one replay at a time).

## Discord bot

1. https://discord.com/developers/applications → New Application → Bot → Reset Token → `DISCORD_BOT_TOKEN`.
2. OAuth2 → URL Generator: scopes `bot`, `applications.commands`; permissions View Channels, Send Messages,
   Embed Links. Open the URL to add the bot to your server.

Optional in `.env`: more admins in `ADMIN_USERS` (the app's owner is one already), and `DISCORD_GUILD_ID` to
register commands on one server only.

## Commands

- `/watch cars name:Parents make:Skoda, Toyota price_max:7000 channel:#monitoring`
- `/watch flats name:Rent region:Rīga deal:Izīrē rooms_min:2`
- `keywords:quattro, 4x4` (cars): only ads whose text has one of the words.
- `/edit cars name:Parents price_max:9000 clear:model` changes only what you give.
- `/searches`, `/unwatch`, `/matches` (recent alerts), `/dashboard` (browser login link).

Alerts go to your DMs, or to `channel:` if you can post there. `/edit … clear:channel` moves them back to DMs.

Admins: `/setup status-channel`, `/setup status-off`, `/setup show` for the service's start/stop/error messages.

## Price watches

In the dashboard, **Price watches → New watch**: paste a product page (or a JSON API address),
click the price on the page, choose "drops by 5%" or "at or below a price", and save. The page is
read every 30 minutes to daily; alerts go to your DMs or a channel. One read serves everyone
watching the same page.

## Metrics

Every service serves Prometheus metrics on `METRICS_ADDR` (default `127.0.0.1:9100` on the host; Docker sets `:9100`, inside its network only), and
`make up` starts a Prometheus that scrapes them: http://127.0.0.1:9090 (try `Status > Targets`). The metric list is in
[docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md#metrics).

## Grafana

`make up` also starts Grafana with ready-made dashboards: http://127.0.0.1:3000, user `admin`, password
`GRAFANA_ADMIN_PASSWORD` from `.env` (`make env` generates one). Four dashboards: **Pipeline** (scrape
cycles and freshness, ingest, outbox, matcher, notifier, Kafka and dead letters, service `up`), **Price watches**
(checks and alerts), **API & bot** (HTTP rate, errors, latency, bot commands) and **Containers** (per-container CPU,
memory, network, disk and uptime from cAdvisor, scrape health of cAdvisor/Prometheus/Grafana, Go runtime metrics per
service; filter by compose service). The stat panels at the top turn
red/orange on trouble (a target down, a stale scrape, a dead-letter publish error). Dashboards are read-only in the UI;
how to change them is in [docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md#grafana).

The **Postgres & Kafka** dashboard shows database health (connections, transactions, cache hit ratio, deadlocks,
sizes) and Kafka (consumer lag, throughput, dead-letter topics). It is fed by `postgres-exporter` and `kafka-exporter`,
which `make up` starts too (compose network only). Postgres is read through a separate read-only role, `admon_monitor`
(`pg_monitor`), that a one-shot `pg-monitor-init` container creates or updates on every `make up`.

Upgrading with an existing `.env`: add a line `GRAFANA_ADMIN_PASSWORD=` followed by the output of `openssl rand -hex 16`,
and the same for `MONITOR_DB_PASSWORD=`. Without `GRAFANA_ADMIN_PASSWORD` the Grafana container exits with a message.
Without `MONITOR_DB_PASSWORD`, `pg-monitor-init` only prints a warning (and `make up` a note), and the dashboard shows
Postgres as down (`pg_up` 0) until you add it and run `make up` again. `make up` also reloads Prometheus's config, so
the new scrape jobs apply to an existing stack (the first `up` recreates the `prometheus` container for its new config
mount; with plain `docker compose up`, run `docker compose kill -s HUP prometheus` after later config edits).

## Alerts

Grafana evaluates alert rules (folder `admon`, provisioned from `infra/grafana/provisioning/alerting/`) and sends
them to a Discord channel. They are visible in Grafana under **Alerting > Alert rules** even without Discord.

1. In Discord: channel settings (gear) > Integrations > Webhooks > New Webhook > Copy Webhook URL.
2. Put it in `.env` as `GRAFANA_DISCORD_WEBHOOK_URL=<url>` and run `make up`. Treat the URL like a password.
   Optional: `GRAFANA_ROOT_URL` (default `http://127.0.0.1:3000`) is what the message links point to; set your
   tunnel address if you open Grafana from elsewhere.

Without the variable Grafana still starts and shows the alerts; `make up` prints a note and Grafana logs a failed
delivery per notification. Messages are grouped per alert (first after 30 s, updates every 5 min, repeated every
4 h while it fires) and a "RESOLVED" message follows when it clears.

Critical: an app service down, Kafka or its exporter down, Postgres down (it fires until `MONITOR_DB_PASSWORD` is
set in `.env`: the exporter cannot log in), scraper stale (no good cycle for 15 min), dead-letter publish errors.
Warning: new dead letters (`make dlq TOPIC=...`), consumer lag high, outbox backlog, notifier delivery failures,
price-watch check failures, API 5xx, a container restarting repeatedly, a monitoring component down. Thresholds are
in [docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md#alerting).

To silence an alert during maintenance: click **Silence** in the Discord message, or Grafana > Alerting > Silences >
New silence (match `alertname` or `severity`). To mute a rule for good, set `isPaused: true` on it in
`rules.yml` (provisioned rules are read-only in the UI; see the notes). A Grafana outage cannot alert by itself.

## Dashboard

http://127.0.0.1:8080, log in with `/dashboard`. To reach it from elsewhere, use a tunnel and set `PUBLIC_URL`.
