-- +goose Up
-- A scan is full (every list page read again, sold ones marked) or a
-- recalculation (the stored listings rated again, and the ad pages of the
-- potential buys not read yet).
ALTER TABLE gem_scans ADD COLUMN mode TEXT NOT NULL DEFAULT 'full';

-- +goose Down
ALTER TABLE gem_scans DROP COLUMN mode;
