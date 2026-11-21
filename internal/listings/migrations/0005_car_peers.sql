-- +goose Up
-- Pricing a car alert reads the stored ads of the same make and model.
CREATE INDEX idx_listings_make_model ON listings (source, (attrs->>'make'), (attrs->>'model'));

-- +goose Down
DROP INDEX idx_listings_make_model;
