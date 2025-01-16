package delta

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chariot-giving/delta/internal/db/sqlc"
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
		UniqueOpts: river.UniqueOpts{
			ByPeriod: time.Hour * 2,
		},
	}
}

// controllerInformerScheduler is a worker that schedules inform jobs for controllers
// this effectively delegates the work to the informer controller workers via river
type controllerInformerScheduler struct {
	pool *pgxpool.Pool
	river.WorkerDefaults[InformScheduleArgs]
}

func (w *controllerInformerScheduler) Work(ctx context.Context, job *river.Job[InformScheduleArgs]) error {
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	queries := sqlc.New(w.pool)

	informers, err := queries.ControllerInformReadyList(ctx, job.Args.InformInterval)
	if err != nil {
		return err
	}

	for _, informer := range informers {
		var opts *InformOptions
		if len(informer.Opts) > 0 {
			err = json.Unmarshal(informer.Opts, opts)
			if err != nil {
				return err
			}
		}
		_, err = riverClient.Insert(ctx, InformArgs{
			ResourceKind:    informer.ResourceKind,
			ProcessExisting: informer.ProcessExisting,
			RunForeground:   informer.RunForeground,
			Options:         opts,
		}, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
