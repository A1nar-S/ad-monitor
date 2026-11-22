-- +goose Up
-- Which listing was already matched to which search; the matcher owns it.
-- It used to be the subscriptions slice's table, with a foreign key to
-- searches: on a database that has it, it is taken over as it is and the key
-- dropped, as no slice references another's tables. A deleted search's
-- matches are left to the purge (search IDs are never reused). No FK to
-- listings either: that table belongs to ingest and is purged on its own.
CREATE TABLE IF NOT EXISTS matches (
    search_id   BIGINT      NOT NULL,
    source      TEXT        NOT NULL,
    external_id TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (search_id, source, external_id)
);
ALTER TABLE matches DROP CONSTRAINT IF EXISTS matches_search_id_fkey;
CREATE INDEX IF NOT EXISTS idx_matches_created ON matches (created_at);

-- +goose Down
DROP TABLE matches;
