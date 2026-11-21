-- +goose Up
-- fingerprint: the item behind an ad, as its source identifies it (a car by
-- make, model, year, engine and mileage), so a deleted and reposted ad is
-- recognised under its new ID. '' = the source cannot tell. Older rows get
-- one when next scraped.
ALTER TABLE listings ADD COLUMN fingerprint TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_listings_fingerprint ON listings (source, fingerprint) WHERE fingerprint <> '';

-- +goose Down
DROP INDEX idx_listings_fingerprint;
ALTER TABLE listings DROP COLUMN fingerprint;
