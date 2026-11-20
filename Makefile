# Day-to-day commands. Everything runs in Docker; Go is only needed for `make test`.
COMPOSE := docker compose --env-file .env -f infra/docker-compose.yml

.PHONY: help env up infra down logs ps purge dlq dlq-replay test

help:
	@echo "make env     create .env with random DB, Grafana and monitoring passwords (then add the Discord values)"
	@echo "make up      build and start everything: Postgres, Kafka, scraper, ingest, matcher, notifier, bot, dashboard"
	@echo "make infra   start only Postgres and Kafka (for go run on the host)"
	@echo "make logs    follow the service logs"
	@echo "make ps      show container status"
	@echo "make purge   delete listings not seen for RETENTION_HOURS, and expired logins"
	@echo "make dlq TOPIC=listings.new          list records a consumer gave up on (dead letters)"
	@echo "make dlq-replay TOPIC=listings.new   republish them to the topic once"
	@echo "make down    stop everything (data is kept in Docker volumes)"

env:
	@test ! -f .env || { echo ".env already exists; not overwriting"; exit 1; }
	@sed -e "s/^DB_PASSWORD=.*/DB_PASSWORD=$$(openssl rand -hex 24)/" -e "s/^GRAFANA_ADMIN_PASSWORD=.*/GRAFANA_ADMIN_PASSWORD=$$(openssl rand -hex 16)/" -e "s/^MONITOR_DB_PASSWORD=.*/MONITOR_DB_PASSWORD=$$(openssl rand -hex 16)/" .env.example > .env
	@chmod 600 .env
	@echo "created .env; now set DISCORD_BOT_TOKEN, ADMIN_USERS and ADMIN_CHANNEL_ID"

up: .env
	@grep -q '^MONITOR_DB_PASSWORD=.' .env || echo "note: .env has no MONITOR_DB_PASSWORD; add MONITOR_DB_PASSWORD=<openssl rand -hex 16> for Postgres metrics (see README)"
	$(COMPOSE) --profile app up -d --build
	@# Pick up an edited infra/prometheus/prometheus.yml without recreating the container.
	@$(COMPOSE) --profile app kill -s HUP prometheus >/dev/null 2>&1 || true

infra: .env
	$(COMPOSE) up -d

down:
	$(COMPOSE) --profile app down

logs:
	$(COMPOSE) --profile app logs -f --tail=100 scraper ingest matcher notifier bot api

ps:
	$(COMPOSE) --profile app ps

purge:
	$(COMPOSE) --profile tools run --rm --build admin purge

dlq:
	@test -n "$(TOPIC)" || { echo "usage: make dlq TOPIC=listings.discovered|listings.new|notifications.requested"; exit 1; }
	$(COMPOSE) --profile tools run --rm --build admin dlq list $(TOPIC)

dlq-replay:
	@test -n "$(TOPIC)" || { echo "usage: make dlq-replay TOPIC=listings.discovered|listings.new|notifications.requested"; exit 1; }
	$(COMPOSE) --profile tools run --rm --build admin dlq replay $(TOPIC)

test:
	go test ./cmd/... ./internal/...

.env:
	@echo "missing .env: run 'make env' first"; exit 1
