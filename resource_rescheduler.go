package delta

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type RescheduleArgs struct {
}

func (r RescheduleArgs) Kind() string {
	return "delta.maintenance.reschedule"
}

func (r RescheduleArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "maintenance",
	}
}

type rescheduler struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	river.WorkerDefaults[RescheduleArgs]
}

func (r *rescheduler) Work(ctx context.Context, job *river.Job[RescheduleArgs]) error {
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	errorData, err := json.Marshal(deltatype.AttemptError{
		At:      now,
		Attempt: max(job.Attempt, 0),
		Error:   "Expired resource reset by Resource Re-scheduler",
		Trace:   "TODO",
	})
	if err != nil {
		return fmt.Errorf("error marshaling error JSON: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := sqlc.New(tx)

	resources, err := queries.ResourceResetExpired(ctx, errorData)
	if err != nil {
		r.logger.Error("error resetting expired resources", "error", err)
		return err
	}

	if len(resources) > 0 {
		insertParams := make([]river.InsertManyParams, len(resources))
		for i, resource := range resources {
			insertParams[i] = river.InsertManyParams{
				Args: ScheduleArgs[kindObject]{
					ResourceID: resource.ID,
					object:     kindObject{kind: resource.Kind},
				},
			}
		}

		_, err = riverClient.InsertManyTx(ctx, tx, insertParams)
		if err != nil {
			r.logger.Error("error inserting rescheduled resources", "error", err)
			return err
		}

		r.logger.Info("rescheduled expired resources", "count", len(resources))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}
