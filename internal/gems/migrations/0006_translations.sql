-- +goose Up
-- A gem's texts in other languages than the default (English) one:
-- {"lv": {"overall": "…", "fields": [...], "why": {"price": "…"}}}.
ALTER TABLE gems ADD COLUMN translations JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE gems DROP COLUMN translations;
