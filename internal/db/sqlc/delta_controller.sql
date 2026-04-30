-- name: ControllerCreateOrSetUpdatedAt :one
INSERT INTO delta_controller(
        created_at,
        metadata,
        name,
        updated_at,
        inform_interval,
        reconcile_interval
    )
VALUES (
        now(),
        coalesce(@metadata::jsonb, '{}'::jsonb),
        @name::text,
        coalesce(sqlc.narg('updated_at')::timestamptz, now()),
        coalesce(
            sqlc.narg('inform_interval')::interval,
            '1 hour'::interval
        ),
        coalesce(
            sqlc.narg('reconcile_interval')::interval,
            '0'::interval
        )
    ) ON CONFLICT (name) DO
UPDATE
SET updated_at = coalesce(sqlc.narg('updated_at')::timestamptz, now()),
    inform_interval = coalesce(
        sqlc.narg('inform_interval')::interval,
        '1 hour'::interval
    ),
    reconcile_interval = coalesce(
        sqlc.narg('reconcile_interval')::interval,
        '0'::interval
    )
RETURNING *;
-- name: ControllerListReady :many
SELECT *
FROM delta_controller
WHERE inform_interval > '0'
    AND now() - last_inform_time > inform_interval
ORDER BY last_inform_time ASC;
-- name: ControllerListReadyForReconcile :many
SELECT *
FROM delta_controller
WHERE reconcile_interval > '0'
    AND now() - last_reconcile_time > reconcile_interval
ORDER BY last_reconcile_time ASC;
-- name: ControllerSetLastInformTime :exec
UPDATE delta_controller
SET last_inform_time = @last_inform_time,
    updated_at = now()
WHERE name = @name;
-- name: ControllerSetLastReconcileTime :exec
UPDATE delta_controller
SET last_reconcile_time = @last_reconcile_time,
    updated_at = now()
WHERE name = @name;
-- name: ControllerGet :one
SELECT *
FROM delta_controller
WHERE name = @name;
