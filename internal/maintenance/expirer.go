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
	pool *pgxpool.Pool
	river.WorkerDefaults[ExpireResourceArgs]
}

func NewNamespaceExpirer(pool *pgxpool.Pool) *namespaceExpirer {
	return &namespaceExpirer{pool: pool}
}

func (e *namespaceExpirer) Work(ctx context.Context, job *river.Job[ExpireResourceArgs]) error {
	logger := middleware.LoggerFromContext(ctx).WithGroup("maintenance").With("name", "expirer")
	queries := sqlc.New(e.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}

	logger.Debug("expiring resources for all namespaces", "num_namespaces", len(namespaces))

	for _, namespace := range namespaces {
		// don't expire resources in a namespace that has no expiry ttl
		if namespace.ExpiryTtl == 0 {
			continue
		}

		logger.Debug("expiring resources for namespace", "namespace", namespace.Name)

		numExpired, err := queries.ResourceExpire(ctx, &sqlc.ResourceExpireParams{
			Namespace: namespace.Name,
			ExpiryTtl: namespace.ExpiryTtl,
		})
		if err != nil {
			logger.Error("failed to expire resources for namespace", "namespace", namespace.Name, "error", err)
			return err
		}

		logger.Info("expired resources for namespace", "namespace", namespace.Name, "num_expired", numExpired)
	}

	logger.Debug("finished expiring resources for all namespaces", "num_namespaces", len(namespaces))

	return nil
}
