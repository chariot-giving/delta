package maintenance

import (
	"context"
	"log/slog"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type ExpireResourceArgs struct {
}

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
	pool   *pgxpool.Pool
	logger *slog.Logger
	river.WorkerDefaults[ExpireResourceArgs]
}

func NewNamespaceExpirer(pool *pgxpool.Pool, logger *slog.Logger) *namespaceExpirer {
	return &namespaceExpirer{pool: pool, logger: logger}
}

func (e *namespaceExpirer) Work(ctx context.Context, job *river.Job[ExpireResourceArgs]) error {
	queries := sqlc.New(e.pool)

	namespaces, err := queries.NamespaceList(ctx)
	if err != nil {
		return err
	}

	expireParams := make([]*sqlc.ResourceExpireParams, 0, len(namespaces))
	for _, namespace := range namespaces {
		// don't expire resources in a namespace that has no expiry ttl
		if namespace.ExpiryTtl == 0 {
			continue
		}

		e.logger.Debug("expiring resources for namespace", "namespace", namespace.Name)
		expireParams = append(expireParams, &sqlc.ResourceExpireParams{
			Namespace: namespace.Name,
			ExpiryTtl: namespace.ExpiryTtl,
		})
	}

	batch := queries.ResourceExpire(ctx, expireParams)

	batch.Exec(func(i int, err error) {
		if err != nil {
			e.logger.Error("failed to expire resources for namespace", "namespace", expireParams[i].Namespace, "error", err)
			return
		}
		e.logger.Debug("expired resources for namespace", "namespace", expireParams[i].Namespace)
	})

	return nil
}
