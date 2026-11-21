# Advertisement monitoring

Watches ss.com (cars, flats) and pinned product pages, and sends new matching ads and price drops to Discord.
Details: [docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md). Plans: [docs/ROADMAP.md](docs/ROADMAP.md).

## Run

Needs Docker, `make`, `openssl`.

```sh
make env    # creates .env; then set DISCORD_BOT_TOKEN
make up     # start everything
make logs   # make down stops it
```

Dead letters: `make dlq TOPIC=listings.new` lists them, `make dlq-replay TOPIC=listings.new` resends them.

Deploying to a server: [deploy/README.md](deploy/README.md) (k3s, dashboard public via Cloudflare Tunnel).

## Discord bot

1. https://discord.com/developers/applications → New Application → Bot → Reset Token → `DISCORD_BOT_TOKEN`.
2. OAuth2 → URL Generator: scopes `bot`, `applications.commands`; permissions View Channels, Send Messages,
   Embed Links. Open the URL to add the bot.

Optional: extra admins in `ADMIN_USERS`, `DISCORD_GUILD_ID` to register commands on one server only.

## Commands

- `/watch cars name:Parents make:Skoda, Toyota price_max:7000 channel:#monitoring`
- `/watch flats name:Rent region:Rīga deal:Izīrē rooms_min:2`
- `/edit cars name:Parents price_max:9000 clear:model`
- `/searches`, `/unwatch`, `/matches`, `/dashboard` (login link)
- Admins: `/setup status-channel`, `/setup status-off`, `/setup show`

Alerts go to your DMs, or to `channel:`.

## Dashboard

http://127.0.0.1:8080, log in with `/dashboard`. Price watches: **Price watches → New watch**, paste a product
page, click the price, pick a rule.

## Monitoring

- Prometheus: http://127.0.0.1:9090
- Grafana: http://127.0.0.1:3000, user `admin`, password `GRAFANA_ADMIN_PASSWORD` from `.env`. Dashboards for the
  pipeline, price watches, API & bot, containers, Postgres & Kafka.
- Alerts: set `GRAFANA_DISCORD_WEBHOOK_URL` in `.env` (a Discord channel webhook) and `make up`. Without it they show
  in Grafana only.

Rules, thresholds and how to change dashboards: [docs/PROJECT_NOTES.md](docs/PROJECT_NOTES.md#alerting).
