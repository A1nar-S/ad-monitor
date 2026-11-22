-- name: InsertWatchTarget :one
-- A new target starts from the reading the user picked the price on. On
-- conflict nothing is returned: the target exists; use GetWatchTargetID.
INSERT INTO watch_targets (url, extractor, title, image_url, checked_at, ok_at, price_cents, currency, raw, next_check_at)
VALUES (@url, @extractor, @title, @image_url, now(), now(), @price_cents, @currency, @raw,
        now() + make_interval(mins => @interval_minutes::int))
ON CONFLICT (url, extractor) DO NOTHING
RETURNING id;

-- name: GetWatchTargetID :one
SELECT id FROM watch_targets WHERE url = @url AND extractor = @extractor;

-- name: PullWatchTarget :exec
-- A new watch on an existing target is read no later than its own interval.
UPDATE watch_targets SET next_check_at = LEAST(next_check_at, now() + make_interval(mins => @interval_minutes::int))
WHERE id = @id;

-- name: GetWatchTargetState :one
SELECT id, url, extractor, price_cents, currency, failures, failing, ok_at, checked_at
FROM watch_targets WHERE id = @id;

-- name: LockWatchTarget :one
SELECT id, url, price_cents, currency, failures, failing, ok_at
FROM watch_targets WHERE id = @id FOR UPDATE;

-- name: DeleteOrphanWatchTarget :exec
-- A target nobody watches any more goes, with its history.
DELETE FROM watch_targets t WHERE t.id = @id AND NOT EXISTS (SELECT 1 FROM watches w WHERE w.target_id = t.id);

-- name: ClaimDueWatchTargets :many
-- Leases the targets due for a read by moving their next check past the
-- lease: a reader that dies mid-read leaves them to be read again then.
UPDATE watch_targets t SET next_check_at = now() + make_interval(secs => @lease_seconds::float8)
WHERE t.id IN (
    SELECT d.id FROM watch_targets d
    WHERE d.next_check_at <= now()
      AND EXISTS (SELECT 1 FROM watches w WHERE w.target_id = d.id AND w.enabled)
    ORDER BY d.next_check_at
    LIMIT @max_rows
    FOR UPDATE SKIP LOCKED)
RETURNING t.id, t.url, t.extractor;

-- name: ClaimWatchTarget :one
-- Leases one target for a read now, unless it was read in the last
-- min_age_seconds (a "check now" button pressed twice).
UPDATE watch_targets SET next_check_at = now() + make_interval(secs => @lease_seconds::float8)
WHERE id = @id AND (checked_at IS NULL OR checked_at < now() - make_interval(secs => @min_age_seconds::float8))
RETURNING id, url, extractor;

-- name: SaveWatchTargetOK :exec
UPDATE watch_targets SET
    price_cents = @price_cents, currency = @currency, raw = @raw, ok_at = now(), checked_at = now(),
    failures = 0, failing = FALSE, last_error = '',
    title = CASE WHEN @title::text = '' THEN title ELSE @title::text END,
    image_url = CASE WHEN @image_url::text = '' THEN image_url ELSE @image_url::text END,
    next_check_at = now() + make_interval(secs => @next_in_seconds::float8)
WHERE id = @id;

-- name: SaveWatchTargetFailure :exec
UPDATE watch_targets SET
    checked_at = now(), failures = @failures, failing = @failing, last_error = @last_error,
    next_check_at = now() + make_interval(secs => @next_in_seconds::float8)
WHERE id = @id;

-- name: InsertWatchPrice :exec
INSERT INTO watch_prices (target_id, ok, price_cents, currency, raw, error)
VALUES (@target_id, @ok, @price_cents, @currency, @raw, @error);

-- name: ListWatchPrices :many
SELECT at, ok, price_cents, currency, raw, error FROM watch_prices
WHERE target_id = @target_id ORDER BY at DESC, id DESC LIMIT @max_rows;

-- name: CountUserWatches :one
SELECT count(*) FROM watches WHERE user_id = @user_id;

-- name: InsertWatch :one
INSERT INTO watches (user_id, channel_id, target_id, name, rule, target_cents, drop_percent, reference_cents, below, interval_minutes)
VALUES (@user_id, @channel_id, @target_id, @name, @rule, @target_cents, @drop_percent, @reference_cents, @below, @interval_minutes)
RETURNING id;

-- name: UpdateWatch :execrows
UPDATE watches SET
    channel_id = @channel_id, target_id = @target_id, name = @name, rule = @rule, target_cents = @target_cents,
    drop_percent = @drop_percent, reference_cents = @reference_cents,
    reference_at = CASE WHEN @reset_reference::boolean THEN now() ELSE reference_at END,
    below = @below, interval_minutes = @interval_minutes, updated_at = now()
WHERE id = @id;

-- name: SetWatchEnabled :execrows
UPDATE watches SET enabled = @enabled, updated_at = now() WHERE id = @id;

-- name: SetWatchBelow :exec
UPDATE watches SET below = @below WHERE id = @id;

-- name: DeleteWatch :one
DELETE FROM watches WHERE id = @id RETURNING target_id;

-- name: ListTargetWatches :many
-- The enabled watches of a target, with where their alerts go.
SELECT w.id, w.user_id, w.name, w.rule, w.target_cents, w.drop_percent, w.reference_cents, w.below,
       w.interval_minutes, c.kind AS channel_kind, c.target AS channel_target, u.alert_lang
FROM watches w JOIN channels c ON c.id = w.channel_id JOIN users u ON u.id = w.user_id
WHERE w.target_id = @target_id AND w.enabled
ORDER BY w.id;

-- name: ListWatchViews :many
SELECT w.id, w.user_id, u.display_name AS user_name, w.name, w.enabled, w.rule, w.target_cents, w.drop_percent,
       w.reference_cents, w.reference_at, w.below, w.interval_minutes, w.created_at, w.updated_at,
       w.channel_id, c.kind AS channel_kind, c.target AS channel_target, c.label AS channel_label,
       t.id AS target_id, t.url, t.extractor, t.title, t.image_url, t.checked_at, t.ok_at, t.price_cents,
       t.currency, t.raw, t.failures, t.failing, t.last_error, t.next_check_at,
       (SELECT count(*) FROM watch_alerts a WHERE a.watch_id = w.id AND a.kind = 'drop') AS alerts
FROM watches w
JOIN users u ON u.id = w.user_id
JOIN channels c ON c.id = w.channel_id
JOIN watch_targets t ON t.id = w.target_id
WHERE @everyone::boolean OR w.user_id = @user_id
ORDER BY w.created_at DESC, w.id DESC;

-- name: GetWatchView :one
SELECT w.id, w.user_id, u.display_name AS user_name, w.name, w.enabled, w.rule, w.target_cents, w.drop_percent,
       w.reference_cents, w.reference_at, w.below, w.interval_minutes, w.created_at, w.updated_at,
       w.channel_id, c.kind AS channel_kind, c.target AS channel_target, c.label AS channel_label,
       t.id AS target_id, t.url, t.extractor, t.title, t.image_url, t.checked_at, t.ok_at, t.price_cents,
       t.currency, t.raw, t.failures, t.failing, t.last_error, t.next_check_at,
       (SELECT count(*) FROM watch_alerts a WHERE a.watch_id = w.id AND a.kind = 'drop') AS alerts
FROM watches w
JOIN users u ON u.id = w.user_id
JOIN channels c ON c.id = w.channel_id
JOIN watch_targets t ON t.id = w.target_id
WHERE w.id = @id;

-- name: InsertWatchAlert :exec
INSERT INTO watch_alerts (watch_id, kind, price_cents, payload) VALUES (@watch_id, @kind, @price_cents, @payload);

-- name: ListWatchAlerts :many
SELECT id, kind, price_cents, created_at, published_at FROM watch_alerts
WHERE watch_id = @watch_id ORDER BY created_at DESC, id DESC LIMIT @max_rows;

-- name: ClaimWatchAlerts :many
SELECT id, payload FROM watch_alerts WHERE published_at IS NULL ORDER BY id LIMIT @max_rows FOR UPDATE SKIP LOCKED;

-- name: MarkWatchAlertsPublished :exec
UPDATE watch_alerts SET published_at = now() WHERE id = ANY(@ids::bigint[]);
