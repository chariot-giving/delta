package delta

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

type InformScheduleArgs struct{}

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
	// informJitter is the maximum random delay applied to each controller's
	// inform job. Controllers with fixed inform intervals that become ready on
	// the same scheduler tick would otherwise all run their inform work at the
	// same instant (and stay aligned every interval thereafter), causing
	// periodic CPU spikes. Spreading the jobs across this window desyncs them.
	// A value <= 0 disables jitter.
	informJitter time.Duration
	river.WorkerDefaults[InformScheduleArgs]
}

func (w *controllerInformerScheduler) Work(ctx context.Context, job *river.Job[InformScheduleArgs]) error {
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("failed to get river client: %w", err)
	}

	queries := sqlc.New(w.pool)

	controllers, err := queries.ControllerListReady(ctx)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "found controllers", "size", len(controllers))

	for _, controller := range controllers {
		opts := InformOptions{
			After: &controller.LastInformTime,
		}

		insertOpts := river.InsertOpts{
			Queue: controller.Name, // this ensures the job will be picked up by a client who is configured with this controller
		}
		// Stagger the inform job within the jitter window so controllers that
		// became ready on the same tick don't all run at once. We delay the job
		// rather than touching last_inform_time, so the inform cursor (and which
		// objects get informed) is unaffected.
		if w.informJitter > 0 {
			insertOpts.ScheduledAt = time.Now().Add(rand.N(w.informJitter))
		}

		res, err := riverClient.Insert(ctx, InformArgs[kindObject]{
			ResourceKind:    controller.Name,
			ProcessExisting: false,
			RunForeground:   false,
			Options:         &opts,
			object:          kindObject{kind: controller.Name},
		}, &insertOpts)
		if err != nil {
			return fmt.Errorf("failed to insert inform job: %w", err)
		}

		if res.UniqueSkippedAsDuplicate {
			logger.InfoContext(ctx, "skipped controller inform job", "resource_kind", controller.Name)
		} else {
			logger.InfoContext(ctx, "inserted controller inform job", "resource_kind", controller.Name)
		}
	}

	logger.InfoContext(ctx, "finished scheduling controller inform jobs", "size", len(controllers))

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
