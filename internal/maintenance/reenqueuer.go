package maintenance

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

type ReenqueuerArgs struct {
}

func (r ReenqueuerArgs) Kind() string {
	return "delta.maintenance.reenqueuer"
}

func (r ReenqueuerArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "maintenance",
	}
}

type reenqueuer struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	river.WorkerDefaults[ReenqueuerArgs]
}

func NewReenqueuer(pool *pgxpool.Pool, logger *slog.Logger) *reenqueuer {
	return &reenqueuer{pool: pool, logger: logger}
}

func (r *reenqueuer) Work(ctx context.Context, job *river.Job[ReenqueuerArgs]) error {
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("error getting river client: %w", err)
	}

	now := time.Now().UTC()
	errorData, err := json.Marshal(deltatype.AttemptError{
		At:      now,
		Attempt: max(job.Attempt, 0),
		Error:   "Expired resource re-enqueued by Reenqueuer",
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

	count, err := queries.ResourceCountExpired(ctx)
	if err != nil {
		return fmt.Errorf("error counting expired resources: %w", err)
	}

	errors := make([][]byte, int(count))
	for i := 0; i < int(count); i++ {
		errors[i] = errorData
	}

	batch := queries.ResourceRescheduleExpired(ctx, errors)

	batch.Query(func(batchNum int, resources []*sqlc.DeltaResource, err error) {
		if err != nil {
			r.logger.Error("error rescuing expired resource", "error", err)
			return
		}

		// TODO: this will likely not work since the resource job args should be the generic Resource[T]
		// Will probably need to adopt a pattern more similar to the scheduler (delegator)
		insertParams := make([]river.InsertManyParams, len(resources))
		for i, resource := range resources {
			resourceRow := toResourceRow(resource)
			insertParams[i] = river.InsertManyParams{
				Args:       resourceRow,
				InsertOpts: nil,
			}
		}

		_, err = riverClient.InsertManyTx(ctx, tx, insertParams)
		if err != nil {
			r.logger.Error("error rescuing expired resource", "error", err)
			return
		}

		r.logger.Info("re-enqueued expired resource", "count", len(resources))
	})

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

func toResourceRow(resource *sqlc.DeltaResource) deltatype.ResourceRow {
	return deltatype.ResourceRow{
		ID:            resource.ID,
		ObjectID:      resource.ObjectID,
		ObjectKind:    resource.Kind,
		Namespace:     resource.Namespace,
		EncodedObject: resource.Object,
		Hash:          resource.Hash,
		Metadata:      resource.Metadata,
		CreatedAt:     resource.CreatedAt,
		SyncedAt:      resource.SyncedAt,
		Attempt:       int(resource.Attempt),
		MaxAttempts:   int(resource.MaxAttempts),
		State:         deltatype.ResourceState(resource.State),
		Tags:          resource.Tags,
	}
}
