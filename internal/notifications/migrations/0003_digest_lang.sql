-- +goose Up
-- The language a queued ad's digest is written in (events.AlertLangs).
ALTER TABLE digest_queue ADD COLUMN lang TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE digest_queue DROP COLUMN lang;
