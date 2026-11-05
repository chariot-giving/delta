package delta

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

func TestWarnIfPoolTooSmall(t *testing.T) {
	t.Parallel()

	config, err := pgxpool.ParseConfig("postgres://localhost:5432/unused?pool_max_conns=5")
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), config) // connects lazily
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	warnIfPoolTooSmall(logger, pool, map[string]river.QueueConfig{"a": {MaxWorkers: 1}, "b": {MaxWorkers: 1}})
	require.Empty(t, buf.String())

	warnIfPoolTooSmall(logger, pool, map[string]river.QueueConfig{"a": {MaxWorkers: 3}, "b": {MaxWorkers: 3}})
	require.Contains(t, buf.String(), "recommended_min_conns=9")
}
