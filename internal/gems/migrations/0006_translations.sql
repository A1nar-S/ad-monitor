-- +goose Up
-- lines are a gem's verdicts in parts (tone, label, text), for the page to
-- lay out; translations its texts in other languages than the default
-- (English) one: {"lv": {"overall": "…", "fields": [...], "lines": [...],
-- "why": {"price": "…"}}}.
ALTER TABLE gems
    ADD COLUMN lines        JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN translations JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE gems DROP COLUMN lines, DROP COLUMN translations;
