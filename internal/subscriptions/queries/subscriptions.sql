-- name: AccountUser :one
-- The user a chat account belongs to; the display name follows the platform.
WITH a AS (
    UPDATE accounts SET display_name = @display_name
    WHERE accounts.platform = @platform::text AND accounts.external_id = @external_id::text
    RETURNING user_id
)
UPDATE users SET display_name = @display_name FROM a WHERE users.id = a.user_id
RETURNING users.id;

-- name: CreatePlatformUser :one
-- A chat account's first contact.
INSERT INTO users (name, platform, external_id, display_name)
VALUES (@platform::text || ':' || @external_id::text, @platform::text, @external_id::text, @display_name)
ON CONFLICT (platform, external_id) DO UPDATE SET display_name = EXCLUDED.display_name
RETURNING id;

-- name: InsertAccount :exec
INSERT INTO accounts (platform, external_id, user_id, display_name)
VALUES (@platform, @external_id, @user_id, @display_name)
ON CONFLICT (platform, external_id) DO NOTHING;

-- name: UserAccounts :many
SELECT platform, external_id, display_name FROM accounts WHERE user_id = @user_id ORDER BY created_at, platform;

-- name: GetUser :one
SELECT id, COALESCE(platform, '')::text AS platform, COALESCE(external_id, '')::text AS external_id, display_name
FROM users WHERE id = @id;

-- name: UpsertChannel :one
INSERT INTO channels (user_id, kind, target, label) VALUES (@user_id, @kind, @target, @label)
ON CONFLICT (user_id, kind, target) DO UPDATE SET label = CASE WHEN EXCLUDED.label = '' THEN channels.label ELSE EXCLUDED.label END
RETURNING id;

-- name: ListUserChannels :many
SELECT id, kind, target, label FROM channels WHERE user_id = @user_id ORDER BY id;

-- name: GetUserChannel :one
SELECT id, kind, target FROM channels WHERE id = @id AND user_id = @user_id;

-- name: CountUserSearches :one
SELECT count(*) FROM searches WHERE user_id = @user_id;

-- name: InsertSearch :one
INSERT INTO searches (user_id, channel_id, name, enabled, source, feeds, filters, spec, origin)
VALUES (@user_id, @channel_id, @name, TRUE, @source, @feeds, @filters, @spec, @origin)
RETURNING id;

-- name: UpdateSearch :execrows
UPDATE searches SET
    channel_id = @channel_id, name = @name, source = @source, feeds = @feeds,
    filters = @filters, spec = @spec, updated_at = now()
WHERE id = @id;

-- name: GetSearchOwner :one
SELECT user_id FROM searches WHERE id = @id;

-- name: GetSearchIDByName :one
SELECT id FROM searches WHERE user_id = @user_id AND name = @name;

-- name: SetSearchEnabled :execrows
UPDATE searches SET enabled = @enabled, updated_at = now() WHERE id = @id;

-- name: DeleteSearch :execrows
DELETE FROM searches WHERE id = @id;

-- name: ListSearchViews :many
-- A user's searches, or everyone's for admins.
SELECT s.id, s.user_id, u.display_name AS user_name, s.name, s.enabled, s.source, s.feeds, s.spec,
       s.channel_id, c.kind AS channel_kind, c.target AS channel_target, c.label AS channel_label, s.created_at, s.updated_at,
       (SELECT count(*) FROM matches m WHERE m.search_id = s.id) AS matches
FROM searches s
JOIN users u ON u.id = s.user_id
JOIN channels c ON c.id = s.channel_id
WHERE @everyone::boolean OR s.user_id = @user_id
ORDER BY s.created_at DESC, s.id DESC;

-- name: GetSearchView :one
SELECT s.id, s.user_id, u.display_name AS user_name, s.name, s.enabled, s.source, s.feeds, s.spec,
       s.channel_id, c.kind AS channel_kind, c.target AS channel_target, c.label AS channel_label, s.created_at, s.updated_at,
       (SELECT count(*) FROM matches m WHERE m.search_id = s.id) AS matches
FROM searches s
JOIN users u ON u.id = s.user_id
JOIN channels c ON c.id = s.channel_id
WHERE s.id = @id;

-- name: ListUserMatches :many
SELECT m.search_id, s.name AS search_name, m.source, m.external_id, m.created_at
FROM matches m JOIN searches s ON s.id = m.search_id
WHERE s.user_id = @user_id
  AND (sqlc.narg('search_id')::bigint IS NULL OR m.search_id = sqlc.narg('search_id'))
ORDER BY m.created_at DESC
LIMIT @max_rows;

-- name: ListEnabledSearches :many
SELECT s.id, s.user_id, s.name, s.source, s.feeds, s.filters,
       c.kind AS channel_kind, c.target AS channel_target
FROM searches s
JOIN channels c ON c.id = s.channel_id
WHERE s.enabled AND s.source = @source;

-- name: ListEnabledFeeds :many
SELECT DISTINCT unnest(feeds)::text AS feed FROM searches WHERE enabled AND source = @source;

-- name: ListMatchedSearchIDs :many
SELECT search_id FROM matches WHERE source = @source AND external_id = @external_id;

-- name: RecordMatch :exec
INSERT INTO matches (search_id, source, external_id) VALUES (@search_id, @source, @external_id)
ON CONFLICT DO NOTHING;

-- name: PurgeMatches :execrows
DELETE FROM matches WHERE created_at < now() - make_interval(secs => @older_than_seconds::float8);

-- name: InsertLoginToken :exec
INSERT INTO login_tokens (token_hash, user_id, expires_at) VALUES (@token_hash, @user_id, @expires_at);

-- name: ConsumeLoginToken :one
-- Single use: the row is gone once read.
DELETE FROM login_tokens WHERE token_hash = @token_hash AND expires_at > now() RETURNING user_id;

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (@token_hash, @user_id, @expires_at);

-- name: GetSessionUser :one
SELECT u.id, COALESCE(u.platform, '')::text AS platform, COALESCE(u.external_id, '')::text AS external_id, u.display_name
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = @token_hash AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: PurgeExpiredAuth :exec
WITH t AS (DELETE FROM login_tokens WHERE expires_at < now()),
     l AS (DELETE FROM link_codes WHERE expires_at < now())
DELETE FROM sessions WHERE expires_at < now();

-- name: GetSetting :one
SELECT value FROM settings WHERE key = @key;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (@key, @value)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: InsertLinkCode :exec
-- A new code replaces the user's earlier ones.
WITH old AS (DELETE FROM link_codes WHERE user_id = @user_id)
INSERT INTO link_codes (code_hash, user_id, expires_at) VALUES (@code_hash, @user_id, @expires_at);

-- name: PeekLinkCode :one
-- Whose code it is, without using it up: shown before the user confirms.
SELECT user_id FROM link_codes WHERE code_hash = @code_hash AND expires_at > now();

-- name: ConsumeLinkCode :one
-- Single use, like login tokens.
DELETE FROM link_codes WHERE code_hash = @code_hash AND expires_at > now() RETURNING user_id;

-- Linking merges one user (absorbed) into another (keeper), in one
-- transaction: the queries below, in order.

-- name: RepointSearchChannels :exec
-- A channel both users have (the same group channel) is kept once: the
-- absorbed user's searches and watches move to the keeper's copy.
UPDATE searches s SET channel_id = k.id
FROM channels a JOIN channels k ON k.kind = a.kind AND k.target = a.target AND k.user_id = @keeper
WHERE a.user_id = @absorbed AND s.channel_id = a.id;

-- name: RepointWatchChannels :exec
UPDATE watches w SET channel_id = k.id
FROM channels a JOIN channels k ON k.kind = a.kind AND k.target = a.target AND k.user_id = @keeper
WHERE a.user_id = @absorbed AND w.channel_id = a.id;

-- name: DeleteDuplicateChannels :exec
DELETE FROM channels a USING channels k
WHERE a.user_id = @absorbed AND k.user_id = @keeper AND k.kind = a.kind AND k.target = a.target;

-- name: MoveChannels :exec
UPDATE channels SET user_id = @keeper WHERE user_id = @absorbed;

-- name: RenameClashingSearches :exec
-- Names are unique per user: a clashing one gets its ID appended.
UPDATE searches a SET name = a.name || ' (' || a.id || ')'
FROM searches k WHERE a.user_id = @absorbed AND k.user_id = @keeper AND k.name = a.name;

-- name: MoveSearches :execrows
UPDATE searches SET user_id = @keeper WHERE user_id = @absorbed;

-- name: RenameClashingWatches :exec
UPDATE watches a SET name = a.name || ' (' || a.id || ')'
FROM watches k WHERE a.user_id = @absorbed AND k.user_id = @keeper AND k.name = a.name;

-- name: MoveWatches :execrows
UPDATE watches SET user_id = @keeper WHERE user_id = @absorbed;

-- name: MoveAccounts :exec
UPDATE accounts SET user_id = @keeper WHERE user_id = @absorbed;

-- name: MoveSessions :exec
-- A dashboard open as the absorbed user stays logged in, as the keeper.
UPDATE sessions SET user_id = @keeper WHERE user_id = @absorbed;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = @id;
