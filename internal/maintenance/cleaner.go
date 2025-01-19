package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
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
// than a certain threshold that are in namespaces where objects are immutable
type cleaner struct {
	pool      *pgxpool.Pool
	batchSize int
	logger    *slog.Logger
	river.WorkerDefaults[CleanResourceArgs]
}

func NewCleaner(pool *pgxpool.Pool, logger *slog.Logger) *cleaner {
	return &cleaner{
		pool:   pool,
		logger: logger,
	}
}

func (c *cleaner) Work(ctx context.Context, job *river.Job[CleanResourceArgs]) error {
	queries := sqlc.New(c.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}

	c.logger.Debug("cleaning resources", "num_namespaces", len(namespaces))

	for _, namespace := range namespaces {
		c.logger.Debug("cleaning resources", "namespace", namespace.Name)

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

		c.logger.Debug("deleted resources", "namespace", namespace.Name, "num_deleted", numDeleted)
	}

	c.logger.Debug("finished cleaning resources")

	return nil
}
