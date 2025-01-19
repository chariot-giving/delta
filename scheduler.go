package delta

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
	"github.com/chariot-giving/delta/internal/object"
)

type ScheduleArgs[T Object] struct {
	ResourceID int64
	object     T
}

func (s ScheduleArgs[T]) Kind() string {
	return "delta.scheduler." + s.object.Kind()
}

func (s ScheduleArgs[T]) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: "controller",
	}
}

// controllerScheduler is a worker that schedules/enqueues resource jobs for controllers
// the scheduler is similar to the informer, but the scheduler manages enqueuing
// resources based on Delta's internal scheduling logic as opposed to external
// triggers/channels.
type controllerScheduler[T Object] struct {
	factory object.ObjectFactory
	river.WorkerDefaults[ScheduleArgs[T]]
}

func (w *controllerScheduler[T]) Work(ctx context.Context, job *river.Job[ScheduleArgs[T]]) error {
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		return err
	}
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	logger.Debug("scheduling resource", "resource_id", job.Args.ResourceID, "resource_kind", job.Args.object.Kind())

	tx, err := client.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	queries := sqlc.New(tx)

	resource, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:      job.Args.ResourceID,
		Column1: true,
		State:   sqlc.DeltaResourceStateScheduled,
	})
	if err != nil {
		return err
	}

	resourceRow := toResourceRow(resource)
	object := w.factory.Make(&resourceRow)
	if err := object.UnmarshalResource(); err != nil {
		return err
	}

	err = object.Enqueue(ctx, tx, riverClient)
	if err != nil {
		return err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return err
	}

	logger.Info("scheduled resource", "resource_id", job.Args.ResourceID, "resource_kind", job.Args.object.Kind())

	return nil
}
