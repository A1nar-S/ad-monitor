-- +goose Up
-- A scan reads every listing (phase listing), then the ad pages of the
-- potential buys (phase detailing): detail_total of them, detailed so far.
-- progress_at is its last sign of life: a scan takes hours, so one is
-- stuck only once that is old.
ALTER TABLE gem_scans
    ADD COLUMN phase        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN detail_total INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN detailed     INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN progress_at  TIMESTAMPTZ;

-- What the filters and the score breakdown need. gearbox, body and
-- inspection (valid, none) are in internal/deals' words, region in the
-- site's; they are known only once the ad page was read (detailed).
ALTER TABLE gems
    ADD COLUMN gearbox       TEXT             NOT NULL DEFAULT '',
    ADD COLUMN inspection    TEXT             NOT NULL DEFAULT '',
    ADD COLUMN body          TEXT             NOT NULL DEFAULT '',
    ADD COLUMN region        TEXT             NOT NULL DEFAULT '',
    ADD COLUMN engine_l      DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN detailed      BOOLEAN          NOT NULL DEFAULT FALSE,
    ADD COLUMN display_score INTEGER          NOT NULL DEFAULT 0,  -- 0-100
    ADD COLUMN price_diff    DOUBLE PRECISION,                     -- vs similar ads; NULL = not compared
    ADD COLUMN components    JSONB            NOT NULL DEFAULT '[]';

CREATE INDEX idx_gems_body ON gems (source, body);
CREATE INDEX idx_gems_region ON gems (source, region);

-- +goose Down
DROP INDEX idx_gems_region;
DROP INDEX idx_gems_body;
ALTER TABLE gems DROP COLUMN components, DROP COLUMN price_diff, DROP COLUMN display_score, DROP COLUMN detailed,
    DROP COLUMN engine_l, DROP COLUMN region, DROP COLUMN body, DROP COLUMN inspection, DROP COLUMN gearbox;
ALTER TABLE gem_scans DROP COLUMN progress_at, DROP COLUMN detailed, DROP COLUMN detail_total, DROP COLUMN phase;
