-- name: LockSource :exec
-- Held to the end of the transaction: two requests at once (a double click)
-- are checked in turn, so only one scan is created.
SELECT pg_advisory_xact_lock(hashtext('gem_scans|' || @source::text));

-- name: RequestScan :one
-- A new request, unless one is waiting or running. A scan takes hours, so
-- a running one blocks until it shows no progress for 30 minutes (its
-- scraper died); a waiting one for 3 hours. Run under LockSource.
INSERT INTO gem_scans (source)
SELECT @source::text
WHERE NOT EXISTS (
    SELECT 1 FROM gem_scans
    WHERE source = @source::text AND (
        status = 'requested' AND requested_at > now() - interval '3 hours'
        OR status = 'running' AND COALESCE(progress_at, started_at) > now() - interval '30 minutes')
)
RETURNING *;

-- name: ClaimScan :one
-- The oldest waiting request, now running. SKIP LOCKED: two scrapers never
-- claim the same one.
UPDATE gem_scans SET status = 'running', started_at = now(), progress_at = now()
WHERE id = (
    SELECT id FROM gem_scans WHERE status = 'requested' AND source = ANY(@sources::text[])
    ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ScanProgress :exec
UPDATE gem_scans SET phase = 'listing', pages = @pages, ads = @ads, progress_at = now() WHERE id = @id;

-- name: DetailProgress :exec
UPDATE gem_scans SET phase = 'detailing', detail_total = @detail_total, detailed = @detailed, progress_at = now() WHERE id = @id;

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
                  price, price_value, buy, score, overall, fields,
                  gearbox, inspection, body, region, engine_l, detailed, display_score, price_diff, components)
VALUES (@source, @external_id, @scan_id, @title, @url, @image_url, @make, @model, @year, @mileage_km, @fuel,
        @price, @price_value, @buy, @score, @overall, @fields,
        @gearbox, @inspection, @body, @region, @engine_l, @detailed, @display_score, sqlc.narg(price_diff), @components);

-- name: ListGems :many
-- One page of rated cars, best first unless asked otherwise. Empty filters
-- match everything; keep the WHERE in step with CountGems.
SELECT * FROM gems
WHERE source = @source
  AND (@make::text = '' OR make = @make::text)
  AND (@model::text = '' OR model = @model::text)
  AND (@buy::text = '' OR buy = @buy::text)
  AND (cardinality(@fuels::text[]) = 0 OR fuel = ANY(@fuels::text[]))
  AND (cardinality(@gearboxes::text[]) = 0 OR gearbox = ANY(@gearboxes::text[]))
  AND (cardinality(@bodies::text[]) = 0 OR body = ANY(@bodies::text[]))
  AND (cardinality(@regions::text[]) = 0 OR region = ANY(@regions::text[]))
  AND (@min_price::float8 = 0 OR price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (@min_year::int = 0 OR year >= @min_year::int)
  AND (@max_year::int = 0 OR year <= @max_year::int)
  AND (@max_mileage::int = 0 OR (mileage_km > 0 AND mileage_km <= @max_mileage::int))
  AND (@min_engine::float8 = 0 OR engine_l >= @min_engine::float8)
  AND (@max_engine::float8 = 0 OR (engine_l > 0 AND engine_l <= @max_engine::float8))
  AND (NOT @detailed_only::boolean OR detailed)
  AND (NOT @inspected_only::boolean OR inspection = 'valid')
ORDER BY
  CASE WHEN @sort::text = 'price' THEN price_value END ASC,
  CASE WHEN @sort::text = 'year' THEN year END DESC,
  CASE WHEN @sort::text = 'mileage' THEN NULLIF(mileage_km, 0) END ASC NULLS LAST,
  CASE WHEN @sort::text = 'discount' THEN price_diff END ASC NULLS LAST,
  score DESC, price_value ASC, external_id
LIMIT @lim OFFSET @off;

-- name: CountGems :one
SELECT count(*) FROM gems
WHERE source = @source
  AND (@make::text = '' OR make = @make::text)
  AND (@model::text = '' OR model = @model::text)
  AND (@buy::text = '' OR buy = @buy::text)
  AND (cardinality(@fuels::text[]) = 0 OR fuel = ANY(@fuels::text[]))
  AND (cardinality(@gearboxes::text[]) = 0 OR gearbox = ANY(@gearboxes::text[]))
  AND (cardinality(@bodies::text[]) = 0 OR body = ANY(@bodies::text[]))
  AND (cardinality(@regions::text[]) = 0 OR region = ANY(@regions::text[]))
  AND (@min_price::float8 = 0 OR price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (@min_year::int = 0 OR year >= @min_year::int)
  AND (@max_year::int = 0 OR year <= @max_year::int)
  AND (@max_mileage::int = 0 OR (mileage_km > 0 AND mileage_km <= @max_mileage::int))
  AND (@min_engine::float8 = 0 OR engine_l >= @min_engine::float8)
  AND (@max_engine::float8 = 0 OR (engine_l > 0 AND engine_l <= @max_engine::float8))
  AND (NOT @detailed_only::boolean OR detailed)
  AND (NOT @inspected_only::boolean OR inspection = 'valid');

-- name: GemFacets :many
-- What the filters can offer, with how many gems have each: kind is model
-- (value "make|model"), fuel, gearbox, body or region.
SELECT f.kind::text AS kind, f.value::text AS value, count(*)::int AS n
FROM gems g
CROSS JOIN LATERAL (VALUES ('model', g.make || '|' || g.model), ('fuel', g.fuel), ('gearbox', g.gearbox),
                           ('body', g.body), ('region', g.region)) AS f(kind, value)
WHERE g.source = @source AND f.value <> ''
GROUP BY f.kind, f.value
ORDER BY f.kind, f.value;

-- name: DetailedCount :one
-- How many gems had their ad page read, for the hint by the filters.
SELECT count(*) FILTER (WHERE detailed)::int AS detailed, count(*)::int AS total FROM gems WHERE source = @source;
