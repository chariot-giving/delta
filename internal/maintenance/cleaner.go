package maintenance

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

// defaultNamespace mirrors the delta package's default namespace name.
const defaultNamespace = "default"

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
		pool:      pool,
		batchSize: 1000,
	}
}

func (c *cleaner) Work(ctx context.Context, job *river.Job[CleanResourceArgs]) error {
	logger := middleware.LoggerFromContext(ctx).WithGroup("maintenance").With("name", "cleaner")
	queries := sqlc.New(c.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}
	// Resources informed without a namespace live in "default", which may not
	// have a delta_namespace row.
	if !slices.ContainsFunc(namespaces, func(n *sqlc.DeltaNamespace) bool { return n.Name == defaultNamespace }) {
		namespaces = append(namespaces, &sqlc.DeltaNamespace{Name: defaultNamespace})
	}

	logger.DebugContext(ctx, "cleaning resources", "num_namespaces", len(namespaces))

	for _, namespace := range namespaces {
		logger.DebugContext(ctx, "cleaning resources", "namespace", namespace.Name)

		syncedHorizon := time.Now().Add(-job.Args.SyncedResourceRetentionPeriod)
		if namespace.ExpiryTtl > 0 {
			// Synced resources in a namespace with an expiry are expired and
			// re-synced rather than deleted. Deleting them first would mean they
			// never expire.
			syncedHorizon = time.Time{}
		}

		var numDeleted int64
		for {
			// Wrapped in a function so that defers run as expected.
			batchDeleted, err := func() (int64, error) {
				ctx, cancelFunc := context.WithTimeout(ctx, job.Args.Timeout)
				defer cancelFunc()

				return queries.ResourceDeleteBefore(ctx, &sqlc.ResourceDeleteBeforeParams{
					Namespace:                  namespace.Name,
					DeletedFinalizedAtHorizon:  time.Now().Add(-job.Args.DeletedResourceRetentionPeriod),
					SyncedFinalizedAtHorizon:   syncedHorizon,
					DegradedFinalizedAtHorizon: time.Now().Add(-job.Args.DegradedResourceRetentionPeriod),
					Max:                        int64(c.batchSize),
				})
			}()
			if err != nil {
				return err
			}
			numDeleted += batchDeleted
			if batchDeleted < int64(c.batchSize) {
				break
			}
		}

		logger.DebugContext(ctx, "deleted resources", "namespace", namespace.Name, "num_deleted", numDeleted)
	}

	logger.DebugContext(ctx, "finished cleaning resources")

	return nil
}
