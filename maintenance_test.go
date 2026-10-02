package delta

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/maintenance"
)

// insertSyncedResources inserts n resources in namespace, synced at syncedAt.
func insertSyncedResources(t *testing.T, ctx context.Context, client *Client, namespace string, count int, syncedAt time.Time) {
	t.Helper()

	_, err := client.dbPool.Exec(ctx, `
		INSERT INTO delta_resource (object_id, kind, namespace, state, max_attempts, synced_at, attempted_at)
		SELECT $1 || '-' || g, 'test_object', $1, 'synced', 10, $3, $3
		FROM generate_series(1, $2) g`, namespace, count, syncedAt)
	require.NoError(t, err)
}

func countResources(t *testing.T, ctx context.Context, client *Client, namespace, state string) int {
	t.Helper()

	var count int
	require.NoError(t, client.dbPool.QueryRow(ctx,
		"SELECT count(*) FROM delta_resource WHERE namespace = $1 AND state::text = $2", namespace, state).Scan(&count))
	return count
}

func TestCleaner(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{})
	ctx := context.Background()

	queries := sqlc.New(pool)
	_, err := queries.NamespaceCreateOrSetUpdatedAt(ctx, &sqlc.NamespaceCreateOrSetUpdatedAtParams{Name: "plain"})
	require.NoError(t, err)
	_, err = queries.NamespaceCreateOrSetUpdatedAt(ctx, &sqlc.NamespaceCreateOrSetUpdatedAtParams{Name: "expiring", ExpiryTtl: 3600})
	require.NoError(t, err)

	old := time.Now().Add(-48 * time.Hour)
	insertSyncedResources(t, ctx, client, "plain", 2500, old) // more than one batch
	insertSyncedResources(t, ctx, client, "expiring", 10, old)

	err = maintenance.NewCleaner(pool, nil).Work(ctx, &river.Job[maintenance.CleanResourceArgs]{
		JobRow: &rivertype.JobRow{},
		Args: maintenance.CleanResourceArgs{
			DeletedResourceRetentionPeriod:  24 * time.Hour,
			SyncedResourceRetentionPeriod:   24 * time.Hour,
			DegradedResourceRetentionPeriod: 24 * time.Hour,
			Timeout:                         10 * time.Second,
		},
	})
	require.NoError(t, err)

	require.Zero(t, countResources(t, ctx, client, "plain", "synced"))
	// Synced resources in an expiring namespace are left for the expirer.
	require.Equal(t, 10, countResources(t, ctx, client, "expiring", "synced"))
}

func TestExpirer(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{})
	ctx := context.Background()

	_, err := sqlc.New(pool).NamespaceCreateOrSetUpdatedAt(ctx, &sqlc.NamespaceCreateOrSetUpdatedAtParams{Name: "expiring", ExpiryTtl: 3600})
	require.NoError(t, err)
	insertSyncedResources(t, ctx, client, "expiring", 1500, time.Now().Add(-2*time.Hour))
	_, err = pool.Exec(ctx, `
		INSERT INTO delta_resource (object_id, kind, namespace, state, max_attempts, synced_at)
		VALUES ('fresh', 'test_object', 'expiring', 'synced', 10, NOW())`)
	require.NoError(t, err)

	err = maintenance.NewNamespaceExpirer(pool, nil).Work(ctx, &river.Job[maintenance.ExpireResourceArgs]{JobRow: &rivertype.JobRow{}})
	require.NoError(t, err)

	require.Equal(t, 1500, countResources(t, ctx, client, "expiring", "expired"))
	require.Equal(t, 1, countResources(t, ctx, client, "expiring", "synced"))
}

func TestResetExpired_RespectsBatchSize(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{})
	ctx := context.Background()

	insertSyncedResources(t, ctx, client, "default", 3, time.Now())
	_, err := pool.Exec(ctx, "UPDATE delta_resource SET state = 'expired'")
	require.NoError(t, err)

	reset, err := sqlc.New(pool).ResourceResetExpired(ctx, &sqlc.ResourceResetExpiredParams{Error: []byte(`{}`), Max: 2})
	require.NoError(t, err)
	require.Len(t, reset, 2)
	require.Equal(t, 1, countResources(t, ctx, client, "default", "expired"))
}
