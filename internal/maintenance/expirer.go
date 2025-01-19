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

	e.logger.Info("expiring resources for namespaces", "namespaces", namespaces)

	expireParams := make([]*sqlc.ResourceExpireParams, 0, len(namespaces))
	for _, namespace := range namespaces {
		if namespace.ExpiryTtl == 0 {
			continue
		}

		expireParams = append(expireParams, &sqlc.ResourceExpireParams{
			Namespace: namespace.Name,
			ExpiryTtl: namespace.ExpiryTtl,
		})
	}

	// TODO: do we need to call Close() and/or how to handle errors?
	batch := queries.ResourceExpire(ctx, expireParams)

	batch.Exec(func(i int, err error) {
		if err != nil {
			e.logger.Error("failed to expire resources for namespace", "namespace", expireParams[i].Namespace, "error", err)
			return
		}
		e.logger.Info("expired resources for namespace", "namespace", expireParams[i].Namespace, "num_expired", i)
	})

	return nil
}
