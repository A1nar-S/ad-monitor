-- name: LockSource :exec
-- Held to the end of the transaction: two requests at once (a double click)
-- are checked in turn, so only one scan is created.
SELECT pg_advisory_xact_lock(hashtext('gem_scans|' || @source::text));

-- name: RequestScan :one
-- A new request, unless one is waiting or running. One stuck for hours
-- (its scraper died) no longer blocks. Run under LockSource.
INSERT INTO gem_scans (source)
SELECT @source::text
WHERE NOT EXISTS (
    SELECT 1 FROM gem_scans
    WHERE source = @source::text AND status IN ('requested', 'running') AND requested_at > now() - interval '3 hours'
)
RETURNING *;

-- name: ClaimScan :one
-- The oldest waiting request, now running. SKIP LOCKED: two scrapers never
-- claim the same one.
UPDATE gem_scans SET status = 'running', started_at = now()
WHERE id = (
    SELECT id FROM gem_scans WHERE status = 'requested' AND source = ANY(@sources::text[])
    ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ScanProgress :exec
UPDATE gem_scans SET pages = @pages, ads = @ads WHERE id = @id;

-- name: FinishScan :exec
UPDATE gem_scans SET status = @status, finished_at = now(), gone = @gone, rated = @rated, error = @error,
    complete = @complete, note = @note
WHERE id = @id;

-- name: LatestScan :one
SELECT * FROM gem_scans WHERE source = @source ORDER BY id DESC LIMIT 1;

-- name: ClearGems :exec
DELETE FROM gems WHERE source = @source;

-- name: InsertGem :exec
INSERT INTO gems (source, external_id, scan_id, title, url, image_url, make, model, year, mileage_km, fuel,
                  price, price_value, buy, score, overall, fields)
VALUES (@source, @external_id, @scan_id, @title, @url, @image_url, @make, @model, @year, @mileage_km, @fuel,
        @price, @price_value, @buy, @score, @overall, @fields);

-- name: ListGems :many
-- One page of rated cars, best first unless asked otherwise. Empty filters
-- match everything.
SELECT * FROM gems
WHERE source = @source
  AND (@make::text = '' OR make = @make::text)
  AND (@model::text = '' OR model = @model::text)
  AND (@buy::text = '' OR buy = @buy::text)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (@min_year::int = 0 OR year >= @min_year::int)
  AND (@max_mileage::int = 0 OR (mileage_km > 0 AND mileage_km <= @max_mileage::int))
ORDER BY
  CASE WHEN @sort::text = 'price' THEN price_value END ASC,
  CASE WHEN @sort::text = 'year' THEN year END DESC,
  score DESC, price_value ASC, external_id
LIMIT @lim OFFSET @off;

-- name: CountGems :one
SELECT count(*) FROM gems
WHERE source = @source
  AND (@make::text = '' OR make = @make::text)
  AND (@model::text = '' OR model = @model::text)
  AND (@buy::text = '' OR buy = @buy::text)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (@min_year::int = 0 OR year >= @min_year::int)
  AND (@max_mileage::int = 0 OR (mileage_km > 0 AND mileage_km <= @max_mileage::int));

-- name: GemMakes :many
-- The makes and models there are, for the filters.
SELECT make, model, count(*)::int AS n FROM gems WHERE source = @source GROUP BY make, model ORDER BY make, model;
