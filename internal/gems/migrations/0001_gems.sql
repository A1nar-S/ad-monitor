-- +goose Up
-- An admin's request to scan every car for sale on a source, and how far it got.
CREATE TABLE gem_scans (
    id           BIGSERIAL PRIMARY KEY,
    source       TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'requested', -- requested, running, done, failed
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    pages        INTEGER     NOT NULL DEFAULT 0,
    ads          INTEGER     NOT NULL DEFAULT 0,
    gone         INTEGER     NOT NULL DEFAULT 0,
    rated        INTEGER     NOT NULL DEFAULT 0,
    error        TEXT        NOT NULL DEFAULT ''
);

-- The last finished scan's rated cars: a snapshot, replaced by the next one.
CREATE TABLE gems (
    source      TEXT             NOT NULL,
    external_id TEXT             NOT NULL,
    scan_id     BIGINT           NOT NULL,
    title       TEXT             NOT NULL,
    url         TEXT             NOT NULL,
    image_url   TEXT             NOT NULL DEFAULT '',
    make        TEXT             NOT NULL,
    model       TEXT             NOT NULL,
    year        INTEGER          NOT NULL,
    mileage_km  INTEGER          NOT NULL DEFAULT 0,
    fuel        TEXT             NOT NULL DEFAULT '',
    price       TEXT             NOT NULL,
    price_value DOUBLE PRECISION NOT NULL,
    buy         TEXT             NOT NULL, -- good, average, bad, check
    score       DOUBLE PRECISION NOT NULL, -- higher is a better buy
    overall     TEXT             NOT NULL,
    fields      JSONB            NOT NULL,
    PRIMARY KEY (source, external_id)
);

CREATE INDEX idx_gems_rank ON gems (source, score DESC);

-- +goose Down
DROP TABLE gems;
DROP TABLE gem_scans;
