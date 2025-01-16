-- name: ControllerInformCreate :one
INSERT INTO delta_controller_inform (
    resource_kind,
    process_existing,
    run_foreground,
    opts,
    metadata
) VALUES (
    @resource_kind,
    @process_existing,
    @run_foreground,
    @opts,
    @metadata
) RETURNING *;

-- name: ControllerInformReadyList :many
-- Used by periodic scheduler to get list of controller informers that are ready to inform
-- This checks where last_inform_time is null or the interval between now and last_inform_time is greater than the inform_interval
SELECT * FROM delta_controller_inform
WHERE
    run_foreground = false AND
    process_existing = false AND
    (last_inform_time IS NULL OR
    now() - last_inform_time > sqlc.arg('inform_interval')::interval)
ORDER BY last_inform_time ASC;

-- name: ControllerInformSetInformed :exec
-- Used by the actual controller informer to set the last_inform_time and num_resources
UPDATE delta_controller_inform
SET 
    last_inform_time = now(),
    num_resources = @num_resources
WHERE id = @id;
