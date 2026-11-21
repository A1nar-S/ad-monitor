-- name: ScannedOnly :one
-- Whether only a full scan has stored this ad, locking the row to the end of
-- the transaction: of two ingests of it at once, one sees true, then false.
SELECT NOT seen_by_feed FROM listings WHERE source = @source AND external_id = @external_id FOR UPDATE;

-- name: UpsertListing :one
-- Inserts a listing or refreshes a known one. xmax is zero only on a fresh
-- insert, which tells the caller whether the listing is new (see also
-- ScannedOnly).
INSERT INTO listings (
    source, external_id, feed, title, url, location, attrs, price, price_value, image_url, posted_at, fingerprint
) VALUES (
    @source, @external_id, @feed, @title, @url, @location, @attrs, @price, @price_value, @image_url, @posted_at, @fingerprint
)
ON CONFLICT (source, external_id) DO UPDATE SET
    -- The first feed sticks, unless a narrower one now sees the ad (an ad
    -- of transport/cars found on transport/cars/sell): that tells its deal.
    feed         = CASE WHEN listings.feed = '' OR starts_with(EXCLUDED.feed, listings.feed || '/')
                        THEN EXCLUDED.feed ELSE listings.feed END,
    title        = EXCLUDED.title,
    url          = EXCLUDED.url,
    location     = EXCLUDED.location,
    attrs        = EXCLUDED.attrs,
    price        = EXCLUDED.price,
    price_value  = EXCLUDED.price_value,
    image_url    = EXCLUDED.image_url,
    -- A row the source can no longer tell (a mileage removed) keeps its
    -- fingerprint; older rows gain one.
    fingerprint  = CASE WHEN EXCLUDED.fingerprint <> '' THEN EXCLUDED.fingerprint ELSE listings.fingerprint END,
    last_seen_at = now(),
    is_active    = TRUE,
    seen_by_feed = TRUE
RETURNING (xmax = 0)::boolean AS is_new;

-- name: StoreScanned :exec
-- Stores what a full scan saw without announcing it: a new row is marked
-- not yet seen by a feed (see UpsertListing); a known one is refreshed.
INSERT INTO listings (
    source, external_id, feed, title, url, location, attrs, price, price_value, image_url, posted_at, fingerprint, seen_by_feed
) VALUES (
    @source, @external_id, @feed, @title, @url, @location, @attrs, @price, @price_value, @image_url, @posted_at, @fingerprint, FALSE
)
ON CONFLICT (source, external_id) DO UPDATE SET
    feed         = CASE WHEN listings.feed = '' OR starts_with(EXCLUDED.feed, listings.feed || '/')
                        THEN EXCLUDED.feed ELSE listings.feed END,
    title        = EXCLUDED.title,
    url          = EXCLUDED.url,
    location     = EXCLUDED.location,
    attrs        = EXCLUDED.attrs,
    price        = EXCLUDED.price,
    price_value  = EXCLUDED.price_value,
    image_url    = EXCLUDED.image_url,
    fingerprint  = CASE WHEN EXCLUDED.fingerprint <> '' THEN EXCLUDED.fingerprint ELSE listings.fingerprint END,
    last_seen_at = now(),
    is_active    = TRUE;

-- name: UnseenSince :many
-- Active ads not seen since a scan began: gone from the site if the scan
-- covered them (the caller decides, from feed and location).
SELECT external_id, feed, location FROM listings
WHERE source = @source AND is_active AND last_seen_at < @since;

-- name: MarkGoneIDs :execrows
UPDATE listings SET is_active = FALSE WHERE source = @source AND external_id = ANY(@ids::text[]) AND is_active;

-- name: StoredCars :many
-- Every stored ad of a source with a make and a price, ad-page details
-- merged: what a gem scan rates and compares with.
SELECT id, external_id, feed, location, title, url, price, price_value, image_url,
       (attrs || COALESCE(details, '{}'::jsonb))::jsonb AS attrs, posted_at, fingerprint, is_active,
       (detailed_at IS NOT NULL)::boolean AS detailed
FROM listings
WHERE source = @source AND attrs ? 'make' AND price_value IS NOT NULL;

-- name: LockFingerprint :exec
-- Held to the end of the transaction, so two ads of one item ingested at
-- once are seen in turn: the second one finds the first.
SELECT pg_advisory_xact_lock(hashtext(@source::text || '|' || @fingerprint::text));

-- name: EarlierListing :one
-- The latest other stored ad of the same item, with how many there are and
-- when the item was first seen: what a repost alert says about its past.
-- fingerprint <> '' lets the partial index serve it.
SELECT l.external_id, l.price, l.price_value,
       (SELECT count(*) FROM listings o
        WHERE o.source = @source AND o.fingerprint = @fingerprint AND o.fingerprint <> ''
          AND o.external_id <> @external_id)::int AS times,
       (SELECT min(o.first_seen_at) FROM listings o
        WHERE o.source = @source AND o.fingerprint = @fingerprint AND o.fingerprint <> ''
          AND o.external_id <> @external_id)::timestamptz AS first_listed
FROM listings l
WHERE l.source = @source AND l.fingerprint = @fingerprint AND l.fingerprint <> '' AND l.external_id <> @external_id
ORDER BY l.first_seen_at DESC, l.id DESC
LIMIT 1;

-- name: TouchFeed :one
-- Registers a feed on first sight and returns how long ago that was, in
-- seconds. now() is the transaction start, so concurrent callers agree.
INSERT INTO feed_state (source, feed) VALUES (@source, @feed)
ON CONFLICT (source, feed) DO UPDATE SET source = EXCLUDED.source
RETURNING EXTRACT(EPOCH FROM now() - first_seen_at)::float8 AS age_seconds;

-- name: InsertOutbox :exec
INSERT INTO listings_outbox (payload) VALUES (@payload);

-- name: ClaimOutbox :many
SELECT id, payload FROM listings_outbox
WHERE published_at IS NULL
ORDER BY id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxPublished :exec
UPDATE listings_outbox SET published_at = now() WHERE id = ANY(@ids::bigint[]);

-- name: PurgeStaleListings :execrows
-- Keyed on last_seen_at, not first_seen_at: an ad that is still on a feed page
-- must stay known, or the next scrape would announce it as new again.
DELETE FROM listings WHERE last_seen_at < now() - make_interval(secs => @older_than_seconds::float8);

-- name: PurgePublishedOutbox :execrows
DELETE FROM listings_outbox
WHERE published_at IS NOT NULL AND published_at < now() - make_interval(secs => @older_than_seconds::float8);

-- name: KnownIDs :many
-- Which of these ads are already stored. The scraper uses it to see where the
-- ads it saw before begin, also right after a restart. Ads only a full scan
-- stored don't count: to the feeds they are still unseen.
SELECT external_id FROM listings WHERE source = @source AND external_id = ANY(@ids::text[]) AND seen_by_feed;

-- name: FeedSeen :one
SELECT EXISTS (SELECT 1 FROM feed_state WHERE source = @source AND feed = @feed) AS seen;

-- name: GetListings :many
SELECT external_id, title, url, location, price, image_url,
       (attrs || COALESCE(details, '{}'::jsonb))::jsonb AS attrs, first_seen_at, is_active
FROM listings WHERE source = @source AND external_id = ANY(@ids::text[]);

-- name: RecentListings :many
-- The newest stored ads of a source, with the ad-page details merged in: what
-- a search preview is checked against.
SELECT id, external_id, feed, title, url, location, price, price_value, image_url,
       (attrs || COALESCE(details, '{}'::jsonb))::jsonb AS attrs, posted_at, first_seen_at, is_active
FROM listings WHERE source = @source
ORDER BY first_seen_at DESC, id DESC
LIMIT @max_rows;

-- name: GetListingByID :one
SELECT id, source, external_id, feed, title, url, location, price, price_value, image_url,
       (attrs || COALESCE(details, '{}'::jsonb))::jsonb AS attrs, posted_at, first_seen_at, is_active,
       detailed_at
FROM listings WHERE id = @id;

-- name: SaveListingDetails :exec
-- details replaces the previous read: the ad page is the whole truth.
UPDATE listings SET details = @details, detailed_at = now()
WHERE source = @source AND external_id = @external_id;

-- name: MarkListingGone :exec
-- The ad's page says it was removed; the next purge forgets it.
UPDATE listings SET is_active = FALSE WHERE source = @source AND external_id = @external_id;

-- name: CarPeers :many
-- Stored ads to price a car against: same make and model, a year in range.
-- Only the list-page attrs are read, as every ad has them; fuel comes back
-- for the caller to compare in its own terms. The caller also checks each
-- is a car for sale, from feed and location, so the limit leaves room for
-- the wanted and exchange ads it drops. Mileage 0 is unknown; equipment
-- comes from the ad page, so it is [] for an ad whose page was never read.
SELECT feed, location, price_value::float8 AS price,
       (attrs->>'year')::float8 AS year,
       COALESCE(CASE WHEN jsonb_typeof(attrs->'mileage_km') = 'number' THEN (attrs->>'mileage_km')::float8 END, 0)::float8 AS mileage_km,
       COALESCE(attrs->>'fuel', '')::text AS fuel,
       (CASE WHEN jsonb_typeof(details->'equipment') = 'array' THEN details->'equipment' ELSE '[]'::jsonb END)::jsonb AS equipment
FROM listings
WHERE source = @source
  AND attrs->>'make' = @make::text
  AND attrs->>'model' = @model::text
  AND (CASE WHEN jsonb_typeof(attrs->'year') = 'number' THEN (attrs->>'year')::float8 END) BETWEEN @year_min::float8 AND @year_max::float8
  AND price_value IS NOT NULL
  AND external_id <> @exclude::text
  -- The car's own earlier ads (reposts) would price it against itself.
  AND (@fingerprint::text = '' OR fingerprint <> @fingerprint::text)
ORDER BY last_seen_at DESC
LIMIT 2000;
