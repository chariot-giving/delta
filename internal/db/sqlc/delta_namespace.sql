-- name: NamespaceCreateOrSetUpdatedAt :one
INSERT INTO delta_namespace(
    created_at,
    metadata,
    name,
    expiry_ttl,
    updated_at
) VALUES (
    now(),
    coalesce(@metadata::jsonb, '{}'::jsonb),
    @name::text,
    coalesce(@expiry_ttl::integer, 0),
    coalesce(sqlc.narg('updated_at')::timestamptz, now())
) ON CONFLICT (name) DO UPDATE
SET
    updated_at = coalesce(sqlc.narg('updated_at')::timestamptz, now())
RETURNING *;

-- name: NamespaceList :many
SELECT * FROM delta_namespace
ORDER BY updated_at DESC;
