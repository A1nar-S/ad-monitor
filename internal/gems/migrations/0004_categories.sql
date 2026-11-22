-- +goose Up
-- Gems are no longer cars only: a scan is of one category of a source, and
-- what a category filters on lives in attrs (keys as its Spec), not in
-- columns. The cars already rated keep their values.
ALTER TABLE gem_scans ADD COLUMN category TEXT NOT NULL DEFAULT 'cars';

ALTER TABLE gems
    ADD COLUMN category TEXT  NOT NULL DEFAULT 'cars',
    ADD COLUMN heading  TEXT  NOT NULL DEFAULT '',
    ADD COLUMN attrs    JSONB NOT NULL DEFAULT '{}';
UPDATE gems SET heading = make || ' ' || model, attrs = jsonb_strip_nulls(jsonb_build_object(
    'make', make, 'model', model, 'year', year, 'mileage_km', NULLIF(mileage_km, 0), 'engine_l', NULLIF(engine_l, 0),
    'fuel', NULLIF(fuel, ''), 'gearbox', NULLIF(gearbox, ''), 'body', NULLIF(body, ''),
    'inspection', NULLIF(inspection, ''), 'region', NULLIF(region, '')));
-- Rows rated before scores were shown get one from the score they have.
UPDATE gems SET display_score = LEAST(100, GREATEST(0, round(50 + 5 * score)))::int
WHERE display_score = 0 AND score > -1e8;
ALTER TABLE gems DROP CONSTRAINT gems_pkey, ADD PRIMARY KEY (source, category, external_id);
DROP INDEX idx_gems_rank;
DROP INDEX idx_gems_body;
DROP INDEX idx_gems_region;
ALTER TABLE gems DROP COLUMN make, DROP COLUMN model, DROP COLUMN year, DROP COLUMN mileage_km, DROP COLUMN engine_l,
    DROP COLUMN fuel, DROP COLUMN gearbox, DROP COLUMN body, DROP COLUMN inspection, DROP COLUMN region;
CREATE INDEX idx_gems_rank ON gems (source, category, score DESC);
CREATE INDEX idx_gems_attrs ON gems USING gin (attrs);

-- +goose Down
DROP INDEX idx_gems_attrs;
DROP INDEX idx_gems_rank;
ALTER TABLE gems
    ADD COLUMN make       TEXT             NOT NULL DEFAULT '',
    ADD COLUMN model      TEXT             NOT NULL DEFAULT '',
    ADD COLUMN year       INTEGER          NOT NULL DEFAULT 0,
    ADD COLUMN mileage_km INTEGER          NOT NULL DEFAULT 0,
    ADD COLUMN engine_l   DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN fuel       TEXT             NOT NULL DEFAULT '',
    ADD COLUMN gearbox    TEXT             NOT NULL DEFAULT '',
    ADD COLUMN body       TEXT             NOT NULL DEFAULT '',
    ADD COLUMN inspection TEXT             NOT NULL DEFAULT '',
    ADD COLUMN region     TEXT             NOT NULL DEFAULT '';
DELETE FROM gems WHERE category <> 'cars';
UPDATE gems SET make = COALESCE(attrs->>'make', ''), model = COALESCE(attrs->>'model', ''),
    year = COALESCE((attrs->>'year')::float8, 0)::int, mileage_km = COALESCE((attrs->>'mileage_km')::float8, 0)::int,
    engine_l = COALESCE((attrs->>'engine_l')::float8, 0), fuel = COALESCE(attrs->>'fuel', ''),
    gearbox = COALESCE(attrs->>'gearbox', ''), body = COALESCE(attrs->>'body', ''),
    inspection = COALESCE(attrs->>'inspection', ''), region = COALESCE(attrs->>'region', '');
ALTER TABLE gems DROP CONSTRAINT gems_pkey, ADD PRIMARY KEY (source, external_id);
ALTER TABLE gems DROP COLUMN attrs, DROP COLUMN heading, DROP COLUMN category;
CREATE INDEX idx_gems_rank ON gems (source, score DESC);
CREATE INDEX idx_gems_body ON gems (source, body);
CREATE INDEX idx_gems_region ON gems (source, region);
DELETE FROM gem_scans WHERE category <> 'cars';
ALTER TABLE gem_scans DROP COLUMN category;
