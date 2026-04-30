package delta

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

// ReconcileScheduleArgs is a periodic job that scans for controllers whose
// reconciliation interval has elapsed and enqueues an inform job in
// reconciliation mode (ProcessExisting=true, Reconcile=true) for each.
type ReconcileScheduleArgs struct{}

func (s ReconcileScheduleArgs) Kind() string {
	return "delta.scheduler.reconcile"
}

func (s ReconcileScheduleArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "controller",
	}
}

// controllerReconcileScheduler is a worker that schedules reconciliation
// inform jobs for controllers whose reconcile_interval has elapsed.
//
// Mirrors controllerInformerScheduler but on a separate cadence and a
// separate timestamp column, so reconciliation drift checks don't interfere
// with the regular incremental inform's last_inform_time.
type controllerReconcileScheduler struct {
	pool *pgxpool.Pool
	river.WorkerDefaults[ReconcileScheduleArgs]
}

func (w *controllerReconcileScheduler) Work(ctx context.Context, job *river.Job[ReconcileScheduleArgs]) error {
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("failed to get river client: %w", err)
	}

	queries := sqlc.New(w.pool)

	controllers, err := queries.ControllerListReadyForReconcile(ctx)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "found controllers ready for reconciliation", "size", len(controllers))

	for _, controller := range controllers {
		// Bump last_reconcile_time before enqueuing so concurrent leaders
		// don't pile on duplicate reconcile jobs. River's UniqueOpts on
		// InformArgs also de-dupes, but updating the timestamp eagerly
		// keeps ControllerListReadyForReconcile from returning the same
		// row on the next tick.
		if err := queries.ControllerSetLastReconcileTime(ctx, &sqlc.ControllerSetLastReconcileTimeParams{
			LastReconcileTime: time.Now(),
			Name:              controller.Name,
		}); err != nil {
			return fmt.Errorf("failed to set last reconcile time: %w", err)
		}

		opts := InformOptions{}

		res, err := riverClient.Insert(ctx, InformArgs[kindObject]{
			ResourceKind:    controller.Name,
			ProcessExisting: true,
			Reconcile:       true,
			RunForeground:   false,
			Options:         &opts,
			object:          kindObject{kind: controller.Name},
		}, &river.InsertOpts{
			Queue: controller.Name,
		})
		if err != nil {
			return fmt.Errorf("failed to insert reconcile job: %w", err)
		}

		if res.UniqueSkippedAsDuplicate {
			logger.InfoContext(ctx, "skipped controller reconcile job", "resource_kind", controller.Name)
		} else {
			logger.InfoContext(ctx, "inserted controller reconcile job", "resource_kind", controller.Name)
		}
	}

	logger.InfoContext(ctx, "finished scheduling controller reconcile jobs", "size", len(controllers))

	return nil
}
