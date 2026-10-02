-- name: ResourceGetByID :one
SELECT *
FROM delta_resource
WHERE id = @id
LIMIT 1;
-- name: ResourceGetByObjectIDAndKind :one
SELECT *
FROM delta_resource
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
    attempted_at = NOW(),
    synced_at = NULL
FROM locked_resource
WHERE delta_resource.id = locked_resource.id
RETURNING delta_resource.*;
-- name: ResourceSchedule :one
UPDATE delta_resource
SET state = 'scheduled',
    synced_at = NULL,
    object = @object,
    hash = @hash
WHERE id = @id
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
        -- keep only the most recent 20 attempt errors
        WHEN $5::boolean THEN (array_append(errors, $6::jsonb))[GREATEST(1, COALESCE(array_length(errors, 1), 0) - 18):]
        ELSE errors
    END
WHERE id = @id
RETURNING *;
-- name: ResourceCreateOrUpdate :one
INSERT INTO delta_resource (
        object_id,
        kind,
        external_created_at,
        namespace,
        state,
        created_at,
        object,
        metadata,
        tags,
        hash,
        max_attempts,
        synced_at
    )
VALUES ($1, $2, $3, $4, $5, NOW(), $6, $7, $8, $9, $10, NULL) ON CONFLICT (object_id, kind) DO
UPDATE
SET external_created_at = $3,
    state = $5,
    object = $6,
    metadata = $7,
    tags = $8,
    hash = $9,
    max_attempts = $10,
    synced_at = NULL
RETURNING sqlc.embed(delta_resource),
    (xmax = 0) as is_insert;
-- name: ResourceExpire :execrows
-- Update the state of up to @max synced delta_resources to 'expired' based on
-- expiryTTL. (Only synced, expired and deleted resources have a synced_at, so
-- this matches what the previous `state NOT IN ('expired', 'deleted')` did.)
-- Rows locked by a worker are skipped and picked up on a later run.
UPDATE delta_resource
SET state = 'expired'
WHERE id = ANY(ARRAY(
        SELECT dr.id
        FROM delta_resource dr
        WHERE dr.namespace = @namespace
            AND dr.state = 'synced'
            AND dr.synced_at < NOW() - make_interval(secs => @expiry_ttl::integer)
        LIMIT @max::bigint
        FOR UPDATE SKIP LOCKED
    ))
    AND state = 'synced';
-- name: ResourceDeleteBefore :one
WITH deleted_resources AS (
    DELETE FROM delta_resource
    WHERE id = ANY(ARRAY(
            SELECT id
            FROM delta_resource
            WHERE delta_resource.namespace = @namespace
                AND (
                    (
                        state = 'deleted'
                        AND attempted_at < @deleted_finalized_at_horizon::timestamptz
                    )
                    OR (
                        state = 'synced'
                        AND synced_at < @synced_finalized_at_horizon::timestamptz
                    )
                    OR (
                        state = 'degraded'
                        AND attempted_at < @degraded_finalized_at_horizon::timestamptz
                    )
                )
            ORDER BY id
            LIMIT @max::bigint
            FOR UPDATE SKIP LOCKED
        ))
        -- re-check the state in case a row changed before it was locked
        AND state IN ('deleted', 'synced', 'degraded')
    RETURNING id
)
SELECT count(*)
FROM deleted_resources;
-- name: ResourceCountExpired :one
SELECT count(*)
FROM delta_resource
WHERE state = 'expired';
-- name: ResourceResetExpired :many
-- Reset up to @max expired resources to 'pending' so they can be re-scheduled.
-- Rows locked by a worker are skipped and picked up on a later run.
UPDATE delta_resource
SET state = 'pending',
    synced_at = NULL,
    errors = (array_append(errors, @error::jsonb))[GREATEST(1, COALESCE(array_length(errors, 1), 0) - 18):]
WHERE id = ANY(ARRAY(
        SELECT dr.id
        FROM delta_resource dr
        WHERE dr.state = 'expired'
        LIMIT @max::bigint
        FOR UPDATE SKIP LOCKED
    ))
    AND state = 'expired'
RETURNING id, kind;
-- name: ResourceRescueStuckScheduled :many
-- Find resources stuck in the 'scheduled' state past the grace period and reset
-- them to 'pending' so the rescheduler can re-enqueue a scheduler job for them.
-- This protects against orphaning when the corresponding River job is discarded
-- externally.
--
-- A resource only counts as stuck when River has no unfinalized job for it.
-- attempted_at is only set when a resource is worked, so the age check alone
-- also matched resources that were just re-scheduled and still had a job
-- queued, which then got enqueued a second time.
UPDATE delta_resource
SET state = 'pending',
    synced_at = NULL,
    errors = (array_append(errors, @error::jsonb))[GREATEST(1, COALESCE(array_length(errors, 1), 0) - 18):]
WHERE id = ANY(ARRAY(
        SELECT dr.id
        FROM delta_resource dr
        WHERE dr.state = 'scheduled'
            AND COALESCE(dr.attempted_at, dr.created_at) < NOW() - make_interval(secs => @stuck_horizon::integer)
            AND NOT EXISTS (
                SELECT 1
                FROM river_job rj
                WHERE rj.state IN ('available', 'pending', 'retryable', 'running', 'scheduled')
                    AND rj.queue = dr.kind
                    AND rj.kind = 'delta.resource.' || dr.kind
                    AND rj.args->>'ID' = dr.id::text
            )
        LIMIT @max::bigint
        FOR UPDATE OF dr SKIP LOCKED
    ))
    AND state = 'scheduled'
RETURNING id, kind;
-- name: ResourceDegradeOrphanedFailed :execrows
-- Mark 'failed' resources whose River job no longer exists (e.g. it was
-- cancelled or deleted externally) as 'degraded'. Nothing would ever retry
-- them, so without this they stayed 'failed' forever and were never cleaned.
UPDATE delta_resource
SET state = 'degraded'
WHERE id = ANY(ARRAY(
        SELECT dr.id
        FROM delta_resource dr
        WHERE dr.state = 'failed'
            AND COALESCE(dr.attempted_at, dr.created_at) < NOW() - make_interval(secs => @stuck_horizon::integer)
            AND NOT EXISTS (
                SELECT 1
                FROM river_job rj
                WHERE rj.state IN ('available', 'pending', 'retryable', 'running', 'scheduled')
                    AND rj.queue = dr.kind
                    AND rj.kind = 'delta.resource.' || dr.kind
                    AND rj.args->>'ID' = dr.id::text
            )
        FOR UPDATE OF dr SKIP LOCKED
    ))
    AND state = 'failed';
