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
INSERT INTO delta_resource (object_id, kind, namespace, state, created_at, object, metadata, tags, hash)
VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7, $8)
ON CONFLICT (object_id, kind) DO UPDATE
SET state = $4,
    object = $5,
    metadata = $6,
    tags = $7,
    hash = $8
RETURNING *, 
    (xmax = 0) as is_insert;