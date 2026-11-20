-- +goose Up
-- Price watches. A target is one page and the place its price is read from;
-- everyone watching the same thing shares it, so it is read once per
-- interval however many users watch it. A watch is one user's rule about a
-- target.
CREATE TABLE watch_targets (
    id            BIGSERIAL PRIMARY KEY,
    url           TEXT        NOT NULL,
    extractor     JSONB       NOT NULL, -- sources.Extractor
    title         TEXT        NOT NULL DEFAULT '',
    image_url     TEXT        NOT NULL DEFAULT '',
    -- Reads are leased by moving next_check_at forward before reading.
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_at    TIMESTAMPTZ,
    -- The last valid price. It stays when reads fail: a broken page is
    -- never a price of zero.
    ok_at         TIMESTAMPTZ,
    price_cents   BIGINT,
    currency      TEXT        NOT NULL DEFAULT '',
    raw           TEXT        NOT NULL DEFAULT '',
    failures      INT         NOT NULL DEFAULT 0, -- in a row
    failing       BOOLEAN     NOT NULL DEFAULT FALSE, -- watchers were told
    last_error    TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (url, extractor)
);

CREATE INDEX idx_watch_targets_due ON watch_targets (next_check_at);

CREATE TABLE watches (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id       BIGINT  NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    target_id        BIGINT  NOT NULL REFERENCES watch_targets (id),
    name             TEXT    NOT NULL,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    -- below: alert at or below target_cents. drop: alert at or below
    -- reference_cents less drop_percent.
    rule             TEXT    NOT NULL CHECK (rule IN ('below', 'drop')),
    target_cents     BIGINT,
    drop_percent     NUMERIC(5, 2),
    -- The price when the watch started or was last reset; fixed until then.
    reference_cents  BIGINT      NOT NULL,
    reference_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- At or below the threshold at the last valid read. An alert goes out
    -- when this turns true; it re-arms when the price rises above again.
    below            BOOLEAN     NOT NULL DEFAULT FALSE,
    interval_minutes INT         NOT NULL CHECK (interval_minutes > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name),
    CHECK ((rule = 'below' AND target_cents > 0) OR (rule = 'drop' AND drop_percent > 0 AND drop_percent < 100))
);

CREATE INDEX idx_watches_target ON watches (target_id);

-- Price history: one row whenever a target's price or state changes (the
-- price stays what it was between rows), so it stays small.
CREATE TABLE watch_prices (
    id          BIGSERIAL PRIMARY KEY,
    target_id   BIGINT      NOT NULL REFERENCES watch_targets (id) ON DELETE CASCADE,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    ok          BOOLEAN     NOT NULL,
    price_cents BIGINT,
    currency    TEXT        NOT NULL DEFAULT '',
    raw         TEXT        NOT NULL DEFAULT '',
    error       TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX idx_watch_prices_target ON watch_prices (target_id, at DESC);

-- Alerts, written in the same transaction as the reading that caused them
-- and relayed to the notifier from here (an outbox), so none is lost or sent
-- twice. Also the watch's alert history.
CREATE TABLE watch_alerts (
    id           BIGSERIAL PRIMARY KEY,
    watch_id     BIGINT      NOT NULL REFERENCES watches (id) ON DELETE CASCADE,
    kind         TEXT        NOT NULL, -- drop, failing, recovered
    price_cents  BIGINT,
    payload      JSONB       NOT NULL, -- events.NotificationRequested without its key
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX idx_watch_alerts_unpublished ON watch_alerts (id) WHERE published_at IS NULL;
CREATE INDEX idx_watch_alerts_watch ON watch_alerts (watch_id, created_at DESC);

-- +goose Down
DROP TABLE watch_alerts;
DROP TABLE watch_prices;
DROP TABLE watches;
DROP TABLE watch_targets;
