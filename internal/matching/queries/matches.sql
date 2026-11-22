-- name: ListMatchedSearchIDs :many
SELECT search_id FROM matches WHERE source = @source AND external_id = @external_id;

-- name: RecordMatch :exec
INSERT INTO matches (search_id, source, external_id) VALUES (@search_id, @source, @external_id)
ON CONFLICT DO NOTHING;

-- name: RecentMatches :many
SELECT search_id, source, external_id, created_at FROM matches
WHERE search_id = ANY(@search_ids::bigint[])
ORDER BY created_at DESC
LIMIT @max_rows;

-- name: CountMatches :many
SELECT search_id, count(*) AS matches FROM matches
WHERE search_id = ANY(@search_ids::bigint[])
GROUP BY search_id;

-- name: PurgeMatches :execrows
DELETE FROM matches WHERE created_at < now() - make_interval(secs => @older_than_seconds::float8);
