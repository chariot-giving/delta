package delta

import (
	"context"
	"fmt"
	"time"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type InformScheduleArgs struct {
	InformInterval time.Duration
}

func (s InformScheduleArgs) Kind() string {
	return "delta.scheduler.inform"
}

func (s InformScheduleArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "controller",
	}
}

// controllerInformerScheduler is a worker that schedules inform jobs for controllers
// this effectively delegates the work to the informer controller workers via river
// This pattern allows us to have >1 delta client running configured with different controllers
// all pointing and using the same delta database/schema.
// You can think about it as a periodic job delegator/scheduler.
type controllerInformerScheduler struct {
	pool *pgxpool.Pool
	river.WorkerDefaults[InformScheduleArgs]
}

func (w *controllerInformerScheduler) Work(ctx context.Context, job *river.Job[InformScheduleArgs]) error {
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	queries := sqlc.New(w.pool)

	controllers, err := queries.ControllerListReady(ctx, job.Args.InformInterval)
	if err != nil {
		return err
	}

	logger.Info("found controllers", "size", len(controllers))

	for _, controller := range controllers {
		opts := InformOptions{
			Since: &controller.LastInformTime,
		}

		res, err := riverClient.Insert(ctx, InformArgs[kindObject]{
			ResourceKind:    controller.Name,
			ProcessExisting: false,
			RunForeground:   false,
			Options:         &opts,
			object:          kindObject{kind: controller.Name},
		}, nil)
		if err != nil {
			return fmt.Errorf("failed to insert inform job: %w", err)
		}

		if res.UniqueSkippedAsDuplicate {
			logger.Info("skipped controller inform job", "resource_kind", controller.Name)
		} else {
			logger.Info("inserted controller inform job", "resource_kind", controller.Name)
		}
	}

	logger.Info("finished scheduling controller inform jobs", "size", len(controllers))

	return nil
}

// kindObject is a simple struct that implements the Object interface
// this is a hacky way to trick River into inserting a specific job
// based on the resource kind.
type kindObject struct {
	kind string
}

func (k kindObject) Kind() string {
	return k.kind
}

func (k kindObject) ID() string {
	return k.kind
}
