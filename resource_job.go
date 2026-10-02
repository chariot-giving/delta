package delta

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// queuedResourceJobExistsSQL reports whether a resource job for a given
// resource is already waiting to run.
//
// Running jobs are deliberately excluded: a running job may have read the
// resource before the change that triggered this insert, so a follow-up job is
// still needed to pick the change up. The (state, queue, ...) prefix of River's
// prioritized fetching index serves this query, and the set of waiting jobs per
// queue is small.
const queuedResourceJobExistsSQL = `
SELECT EXISTS (
    SELECT 1
    FROM river_job
    WHERE state IN ('available', 'pending', 'retryable', 'scheduled')
        AND queue = $1
        AND kind = $2
        AND args->>'ID' = $3
)`

// insertResourceJobTx enqueues a job to work resource unless one is already
// queued for it, returning whether a job was inserted.
//
// Callers must have locked the resource's delta_resource row in tx (every code
// path that schedules a resource updates the row first). That lock serializes
// concurrent inserters for the same resource, so the existence check can't race
// with another insert.
func insertResourceJobTx[T Object](ctx context.Context, tx pgx.Tx, client *river.Client[pgx.Tx], resource Resource[T], opts *river.InsertOpts) (bool, error) {
	var queued bool
	err := tx.QueryRow(ctx, queuedResourceJobExistsSQL,
		opts.Queue, resource.Kind(), strconv.FormatInt(resource.ID, 10)).Scan(&queued)
	if err != nil {
		return false, fmt.Errorf("failed to check for queued resource job: %w", err)
	}
	if queued {
		return false, nil
	}

	if _, err := client.InsertTx(ctx, tx, resource, opts); err != nil {
		return false, err
	}
	return true, nil
}
