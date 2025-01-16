-- name: NamespaceCreateOrSetUpdatedAt :one
INSERT INTO delta_namespace(
    created_at,
    metadata,
    name,
    resource_expiry,
    updated_at
) VALUES (
    now(),
    coalesce(@metadata::jsonb, '{}'::jsonb),
    @name::text,
    coalesce(@resource_expiry::integer, 0),
    coalesce(sqlc.narg('updated_at')::timestamptz, now())
) ON CONFLICT (name) DO UPDATE
SET
    updated_at = coalesce(sqlc.narg('updated_at')::timestamptz, now())
RETURNING *;
