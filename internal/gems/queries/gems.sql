-- name: LockScans :exec
-- Held to the end of the transaction: two requests at once (a double click)
-- are checked in turn, so only one scan is created.
SELECT pg_advisory_xact_lock(hashtext('gem_scans|' || @source::text || '|' || @category::text));

-- name: RequestScan :one
-- A new request, unless one is waiting or running. A scan takes hours, so
-- a running one blocks until it shows no progress for 30 minutes (its
-- scraper died); a waiting one for 3 hours. Run under LockScans.
INSERT INTO gem_scans (source, category, mode)
SELECT @source::text, @category::text, @mode::text
WHERE NOT EXISTS (
    SELECT 1 FROM gem_scans
    WHERE source = @source::text AND category = @category::text AND (
        status = 'requested' AND requested_at > now() - interval '3 hours'
        OR status = 'running' AND COALESCE(progress_at, started_at) > now() - interval '30 minutes')
)
RETURNING *;

-- name: InterruptScans :execrows
-- A scraper starting up: the scans of its sources still running were its
-- own, cut short when it stopped.
UPDATE gem_scans SET status = 'failed', finished_at = now(), error = 'interrupted: the scraper restarted'
WHERE status = 'running' AND source = ANY(@sources::text[]);

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
SELECT * FROM gem_scans WHERE source = @source AND category = @category ORDER BY id DESC LIMIT 1;

-- name: ClearGems :exec
DELETE FROM gems WHERE source = @source AND category = @category;

-- name: InsertGem :exec
INSERT INTO gems (source, category, external_id, scan_id, title, url, image_url, price, price_value, buy, score,
                  display_score, price_diff, heading, overall, fields, components, detailed, attrs, lines, translations)
VALUES (@source, @category, @external_id, @scan_id, @title, @url, @image_url, @price, @price_value, @buy, @score,
        @display_score, sqlc.narg(price_diff), @heading, @overall, @fields, @components, @detailed, @attrs, @lines, @translations);

-- name: ListGems :many
-- One page of rated listings, best first unless asked otherwise. Empty
-- filters match everything; keep the WHERE in step with CountGems.
-- choices: {"body": ["wagon", "sedan"]}, the attr must be one of them.
-- ranges: {"year": [2015, 0]}, from and to with 0 = open; a "to" also needs
-- the attr known.
SELECT * FROM gems
WHERE source = @source AND category = @category
  AND (@buy::text = '' OR buy = @buy::text)
  AND (@min_price::float8 = 0 OR price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (NOT @detailed_only::boolean OR detailed)
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@choices::jsonb) c
      WHERE NOT COALESCE(c.value ? (attrs->>c.key), FALSE))
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@ranges::jsonb) r
      WHERE (r.value->>0)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) < (r.value->>0)::float8
         OR (r.value->>1)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) NOT BETWEEN 1e-9 AND (r.value->>1)::float8)
ORDER BY
  CASE WHEN @sort::text = 'price' THEN price_value END ASC,
  CASE WHEN @sort::text = 'discount' THEN price_diff END ASC NULLS LAST,
  CASE WHEN @sort_key::text <> '' AND NOT @sort_desc::boolean THEN NULLIF((attrs->>(@sort_key::text))::float8, 0) END ASC NULLS LAST,
  CASE WHEN @sort_key::text <> '' AND @sort_desc::boolean THEN NULLIF((attrs->>(@sort_key::text))::float8, 0) END DESC NULLS LAST,
  score DESC, price_value ASC, external_id
LIMIT @lim OFFSET @off;

-- name: CountGems :one
SELECT count(*) FROM gems
WHERE source = @source AND category = @category
  AND (@buy::text = '' OR buy = @buy::text)
  AND (@min_price::float8 = 0 OR price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (NOT @detailed_only::boolean OR detailed)
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@choices::jsonb) c
      WHERE NOT COALESCE(c.value ? (attrs->>c.key), FALSE))
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@ranges::jsonb) r
      WHERE (r.value->>0)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) < (r.value->>0)::float8
         OR (r.value->>1)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) NOT BETWEEN 1e-9 AND (r.value->>1)::float8);

-- name: GemFacets :many
-- The values each choice attr takes among the gems the other filters let
-- through, with how many have each. keys: {"model": {"parent": "make",
-- "skip": ["model"]}, …}: each attr, the choice it narrows (parent is that
-- one's value, a model's make; "" if none) and the choices its own counts
-- ignore (itself, and those narrowing it). Keep the filters in step with
-- CountGems.
SELECT f.key::text AS key, (g.attrs->>f.key)::text AS value,
       COALESCE(g.attrs->>(f.value->>'parent'), '')::text AS parent, count(*)::int AS n
FROM gems g
CROSS JOIN jsonb_each(@keys::jsonb) AS f
WHERE g.source = @source AND g.category = @category AND g.attrs->>f.key <> ''
  AND (@buy::text = '' OR g.buy = @buy::text)
  AND (@min_price::float8 = 0 OR g.price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR g.price_value <= @max_price::float8)
  AND (NOT @detailed_only::boolean OR g.detailed)
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@choices::jsonb) c
      WHERE NOT (f.value->'skip') ? c.key AND NOT COALESCE(c.value ? (g.attrs->>c.key), FALSE))
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@ranges::jsonb) r
      WHERE (r.value->>0)::float8 > 0 AND COALESCE((g.attrs->>r.key)::float8, 0) < (r.value->>0)::float8
         OR (r.value->>1)::float8 > 0 AND COALESCE((g.attrs->>r.key)::float8, 0) NOT BETWEEN 1e-9 AND (r.value->>1)::float8)
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- name: GemBuyCounts :many
-- How many gems the other filters let through have each verdict. Keep the
-- filters in step with CountGems.
SELECT buy, count(*)::int AS n FROM gems
WHERE source = @source AND category = @category
  AND (@min_price::float8 = 0 OR price_value >= @min_price::float8)
  AND (@max_price::float8 = 0 OR price_value <= @max_price::float8)
  AND (NOT @detailed_only::boolean OR detailed)
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@choices::jsonb) c
      WHERE NOT COALESCE(c.value ? (attrs->>c.key), FALSE))
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_each(@ranges::jsonb) r
      WHERE (r.value->>0)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) < (r.value->>0)::float8
         OR (r.value->>1)::float8 > 0 AND COALESCE((attrs->>r.key)::float8, 0) NOT BETWEEN 1e-9 AND (r.value->>1)::float8)
GROUP BY buy
ORDER BY buy;

-- name: DetailedCount :one
-- How many gems had their ad page read, for the hint by the filters.
SELECT count(*) FILTER (WHERE detailed)::int AS detailed, count(*)::int AS total
FROM gems WHERE source = @source AND category = @category;
