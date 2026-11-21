-- +goose Up
-- complete: the crawl reached the end of the feed and sold cars were marked;
-- note says why not, or what was skipped.
ALTER TABLE gem_scans
    ADD COLUMN complete BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN note     TEXT    NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE gem_scans DROP COLUMN note, DROP COLUMN complete;
