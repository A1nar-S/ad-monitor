-- +goose Up
-- seen_by_feed: false for an ad only a full scan (the admin's gem scan) has
-- stored. The regular pipeline still treats it as unseen, so a scan neither
-- announces old ads nor swallows a new one: the first regular sight is new.
ALTER TABLE listings ADD COLUMN seen_by_feed BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE listings DROP COLUMN seen_by_feed;
