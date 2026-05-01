-- name: ControllerCreateOrSetUpdatedAt :one
INSERT INTO delta_controller(
        created_at,
        metadata,
        name,
        updated_at,
        inform_interval
    )
VALUES (
        now(),
        coalesce(@metadata::jsonb, '{}'::jsonb),
        @name::text,
        coalesce(sqlc.narg('updated_at')::timestamptz, now()),
        coalesce(
            sqlc.narg('inform_interval')::interval,
            '1 hour'::interval
        )
    ) ON CONFLICT (name) DO
UPDATE
SET updated_at = coalesce(sqlc.narg('updated_at')::timestamptz, now()),
    inform_interval = coalesce(
        sqlc.narg('inform_interval')::interval,
        '1 hour'::interval
    )
RETURNING *;
-- name: ControllerListReady :many
SELECT *
FROM delta_controller
WHERE inform_interval > '0'
    AND now() - last_inform_time > inform_interval
ORDER BY last_inform_time ASC;
-- name: ControllerSetLastInformTime :exec
UPDATE delta_controller
SET last_inform_time = @last_inform_time,
    updated_at = now()
WHERE name = @name;
-- name: ControllerGet :one
SELECT *
FROM delta_controller
WHERE name = @name;