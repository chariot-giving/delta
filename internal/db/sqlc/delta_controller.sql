-- name: ControllerCreateOrSetUpdatedAt :one
INSERT INTO delta_controller(
    created_at,
    metadata,
    name,
    updated_at
) VALUES (
    now(),
    coalesce(@metadata::jsonb, '{}'::jsonb),
    @name::text,
    coalesce(sqlc.narg('updated_at')::timestamptz, now())
) ON CONFLICT (name) DO UPDATE
SET
    updated_at = coalesce(sqlc.narg('updated_at')::timestamptz, now())
RETURNING *;

-- name: ControllerListReady :many
SELECT * FROM delta_controller
WHERE
    now() - last_inform_time > sqlc.arg('inform_interval')::interval
ORDER BY last_inform_time ASC;

-- name: ControllerSetLastInformTime :exec
UPDATE delta_controller
SET 
    last_inform_time = now(),
    updated_at = now()
WHERE name = @name;

-- name: ControllerGet :one
SELECT * FROM delta_controller
WHERE name = @name;
