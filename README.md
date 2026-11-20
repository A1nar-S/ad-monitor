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

## Dashboard

http://127.0.0.1:8080, log in with `/dashboard`. To reach it from elsewhere, use a tunnel and set `PUBLIC_URL`.
