-- name: ResourceGetByID :one
SELECT *
FROM delta_resource
WHERE id = @id
LIMIT 1;
-- name: ResourceGetByObjectIDAndKind :one
SELECT * FROM delta_resource
WHERE object_id = @object_id
    AND kind = @kind
LIMIT 1;
-- name: ResourceUpdateAndGetByObjectIDAndKind :one
WITH locked_resource AS (
    SELECT *
    FROM delta_resource dr
    WHERE dr.object_id = $1
        AND dr.kind = $2 FOR
    UPDATE SKIP LOCKED
)
UPDATE delta_resource
SET state = 'pending',
    attempt = delta_resource.attempt + 1,
    attempted_at = NOW()
FROM locked_resource
WHERE delta_resource.id = locked_resource.id
RETURNING delta_resource.*;
-- name: ResourceSetState :one
UPDATE delta_resource
SET state = CASE
        WHEN $1::boolean THEN $2
        ELSE state
    END,
    synced_at = CASE
        WHEN $3::boolean THEN $4
        ELSE synced_at
    END,
    errors = CASE
        WHEN $5::boolean THEN array_append(errors, $6::jsonb)
        ELSE errors
    END
WHERE id = @id
RETURNING *;
-- name: ResourceCreateOrUpdate :one
INSERT INTO delta_resource (object_id, kind, namespace, state, created_at, object, metadata, tags, hash, max_attempts)
VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7, $8, $9)
ON CONFLICT (object_id, kind) DO UPDATE
SET state = $4,
    object = $5,
    metadata = $6,
    tags = $7,
    hash = $8,
    max_attempts = $9
RETURNING sqlc.embed(delta_resource), 
    (xmax = 0) as is_insert;
-- name: ResourceExpire :batchexec
-- Update the state of delta_resources to 'expired' based on expiryTTL
UPDATE delta_resource
SET state = 'expired'
WHERE namespace = @namespace
  AND state NOT IN ('expired', 'deleted') -- Only update resources that are not already expired or deleted
  AND EXTRACT(EPOCH FROM (NOW() - synced_at)) > @expiry_ttl::integer;
-- name: ResourceDeleteBefore :one
WITH deleted_resources AS (
    DELETE FROM delta_resource
    WHERE id IN (
        SELECT id
        FROM delta_resource
        WHERE
            delta_resource.namespace = @namespace
            AND (state = 'deleted' AND synced_at < @deleted_finalized_at_horizon::timestamptz) OR
            (state = 'synced' AND synced_at < @synced_finalized_at_horizon::timestamptz) OR
            (state = 'degraded' AND synced_at < @degraded_finalized_at_horizon::timestamptz)
        ORDER BY id
        LIMIT @max::bigint
    )
    RETURNING *
)
SELECT count(*)
FROM deleted_resources;
-- name: ResourceCountExpired :one
SELECT count(*)
FROM delta_resource
WHERE state = 'expired';
-- name: ResourceResetExpired :many
UPDATE delta_resource
SET 
    state = 'pending',
    synced_at = NULL,
    errors = array_append(errors, @error::jsonb)
WHERE id IN (
    SELECT id
    FROM delta_resource
    WHERE state = 'expired'
)
RETURNING *;