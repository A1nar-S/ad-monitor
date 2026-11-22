-- +goose Up
-- The language a user's alerts are written in: one of events.AlertLangs;
-- '' = not chosen, events.DefaultLang (Latvian).
ALTER TABLE users ADD COLUMN alert_lang TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN alert_lang;
