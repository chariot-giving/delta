package maintenance

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
)

type ExpireResourceArgs struct{}

func (e ExpireResourceArgs) Kind() string {
	return "delta.maintenance.expirer"
}

func (e ExpireResourceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       "maintenance",
	}
}

type namespaceExpirer struct {
	pool      *pgxpool.Pool
	batchSize int
	river.WorkerDefaults[ExpireResourceArgs]
}

func NewNamespaceExpirer(pool *pgxpool.Pool) *namespaceExpirer {
	return &namespaceExpirer{pool: pool, batchSize: 1000}
}

func (e *namespaceExpirer) Work(ctx context.Context, job *river.Job[ExpireResourceArgs]) error {
	logger := middleware.LoggerFromContext(ctx).WithGroup("maintenance").With("name", "expirer")
	queries := sqlc.New(e.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}

	logger.DebugContext(ctx, "expiring resources for all namespaces", "num_namespaces", len(namespaces))

	for _, namespace := range namespaces {
		// don't expire resources in a namespace that has no expiry ttl
		if namespace.ExpiryTtl == 0 {
			continue
		}

		logger.DebugContext(ctx, "expiring resources for namespace", "namespace", namespace.Name)

		var numExpired int64
		for {
			batchExpired, err := queries.ResourceExpire(ctx, &sqlc.ResourceExpireParams{
				Namespace: namespace.Name,
				ExpiryTtl: namespace.ExpiryTtl,
				Max:       int64(e.batchSize),
			})
			if err != nil {
				logger.ErrorContext(ctx, "failed to expire resources for namespace", "namespace", namespace.Name, "error", err)
				return err
			}
			numExpired += batchExpired
			if batchExpired < int64(e.batchSize) {
				break
			}
		}

		logger.InfoContext(ctx, "expired resources for namespace", "namespace", namespace.Name, "num_expired", numExpired)
	}

	logger.DebugContext(ctx, "finished expiring resources for all namespaces", "num_namespaces", len(namespaces))

	return nil
}
