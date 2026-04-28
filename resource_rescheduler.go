package delta

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

type RescheduleResourceArgs struct{}

func (r RescheduleResourceArgs) Kind() string {
	return "delta.scheduler.resources"
}

func (r RescheduleResourceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "maintenance",
	}
}

type rescheduler struct {
	pool                    *pgxpool.Pool
	stuckScheduledThreshold time.Duration
	river.WorkerDefaults[RescheduleResourceArgs]
}

func (r *rescheduler) Work(ctx context.Context, job *river.Job[RescheduleResourceArgs]) error {
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("failed to get river client: %w", err)
	}

	now := time.Now().UTC()
	expiredErrorData, err := json.Marshal(deltatype.AttemptError{
		At:      now,
		Attempt: max(job.Attempt, 0),
		Error:   "Expired resource reset by Resource Re-scheduler",
		Trace:   "TODO",
	})
	if err != nil {
		return fmt.Errorf("error marshaling error JSON: %w", err)
	}

	stuckErrorData, err := json.Marshal(deltatype.AttemptError{
		At:      now,
		Attempt: max(job.Attempt, 0),
		Error:   "Stuck scheduled resource rescued by Resource Re-scheduler",
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

	expired, err := queries.ResourceResetExpired(ctx, expiredErrorData)
	if err != nil {
		logger.ErrorContext(ctx, "error resetting expired resources", "error", err)
		return err
	}

	stuckHorizonSeconds := int32(r.stuckScheduledThreshold.Seconds())
	rescued, err := queries.ResourceRescueStuckScheduled(ctx, &sqlc.ResourceRescueStuckScheduledParams{
		Error:        stuckErrorData,
		StuckHorizon: stuckHorizonSeconds,
	})
	if err != nil {
		logger.ErrorContext(ctx, "error rescuing stuck scheduled resources", "error", err)
		return err
	}

	resources := make([]*sqlc.DeltaResource, 0, len(expired)+len(rescued))
	resources = append(resources, expired...)
	resources = append(resources, rescued...)

	if len(resources) > 0 {
		insertParams := make([]river.InsertManyParams, len(resources))
		for i, resource := range resources {
			logger.DebugContext(ctx, "re-scheduling resource", "resource_id", resource.ID, "resource_kind", resource.Kind)
			insertParams[i] = river.InsertManyParams{
				Args: ScheduleArgs[kindObject]{
					ResourceID: resource.ID,
					object:     kindObject{kind: resource.Kind},
				},
				InsertOpts: &river.InsertOpts{
					Queue: resource.Kind,
				},
			}
		}

		_, err = riverClient.InsertManyTx(ctx, tx, insertParams)
		if err != nil {
			logger.ErrorContext(ctx, "error inserting rescheduled resources", "error", err)
			return err
		}

		logger.InfoContext(ctx, "rescheduled resources", "expired", len(expired), "rescued_stuck", len(rescued))
	} else {
		logger.DebugContext(ctx, "no resources to reschedule")
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}
