package maintenance

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

type CleanResourceArgs struct {
	// DeletedResourceRetentionPeriod is the amount of time to keep deleted resources
	// around before they're removed permanently.
	DeletedResourceRetentionPeriod time.Duration

	// SyncedResourceRetentionPeriod is the amount of time to keep synced resources
	// around before they're removed permanently.
	SyncedResourceRetentionPeriod time.Duration

	// DegradedResourceRetentionPeriod is the amount of time to keep degraded resources
	// around before they're removed permanently.
	DegradedResourceRetentionPeriod time.Duration

	// Timeout of the individual queries in the job cleaner.
	Timeout time.Duration
}

func (c CleanResourceArgs) Kind() string {
	return "delta.maintenance.cleaner"
}

func (c CleanResourceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "maintenance",
	}
}

// cleaner is a worker that cleans up expired resources
// cleans up old degraded resources and also synced resources older
// than a certain threshold that are in namespaces where objects are immutable.
type cleaner struct {
	pool      *pgxpool.Pool
	batchSize int
	river.WorkerDefaults[CleanResourceArgs]
}

func NewCleaner(pool *pgxpool.Pool) *cleaner {
	return &cleaner{
		pool: pool,
	}
}

func (c *cleaner) Work(ctx context.Context, job *river.Job[CleanResourceArgs]) error {
	logger := middleware.LoggerFromContext(ctx).WithGroup("maintenance").With("name", "cleaner")
	queries := sqlc.New(c.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}
	namespaces = append(namespaces, &sqlc.DeltaNamespace{
		Name: "default",
	})

	logger.Info("cleaning resources", "num_namespaces", len(namespaces))

	for _, namespace := range namespaces {
		logger.Debug("cleaning resources", "namespace", namespace.Name)

		// Wrapped in a function so that defers run as expected.
		numDeleted, err := func() (int, error) {
			ctx, cancelFunc := context.WithTimeout(ctx, job.Args.Timeout)
			defer cancelFunc()

			deleted, err := queries.ResourceDeleteBefore(ctx, &sqlc.ResourceDeleteBeforeParams{
				Namespace:                  namespace.Name,
				DeletedFinalizedAtHorizon:  time.Now().Add(-job.Args.DeletedResourceRetentionPeriod),
				SyncedFinalizedAtHorizon:   time.Now().Add(-job.Args.SyncedResourceRetentionPeriod),
				DegradedFinalizedAtHorizon: time.Now().Add(-job.Args.DegradedResourceRetentionPeriod),
				Max:                        int64(c.batchSize),
			})
			if err != nil {
				return 0, err
			}
			return int(deleted), nil
		}()
		if err != nil {
			return err
		}

		logger.Debug("deleted resources", "namespace", namespace.Name, "num_deleted", numDeleted)
	}

	logger.Info("finished cleaning resources")

	return nil
}
