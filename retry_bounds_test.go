package delta

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
)

func TestInform_JobMaxAttemptsFollowsResource(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{}) // not started, so jobs stay queued

	result, err := client.Inform(context.Background(), testObject{ObjectID: "max"}, &InformOpts{MaxAttempts: 3})
	require.NoError(t, err)

	var maxAttempts int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT max_attempts FROM river_job WHERE args->>'ID' = $1::bigint::text", result.Resource.ID).Scan(&maxAttempts))
	require.Equal(t, 3, maxAttempts)
}

func TestResourceSetState_KeepsRecentErrors(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{})
	ctx := context.Background()

	result, err := client.Inform(ctx, testObject{ObjectID: "errs"}, nil)
	require.NoError(t, err)

	queries := sqlc.New(pool)
	for i := range 25 {
		_, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
			ID:      result.Resource.ID,
			State:   sqlc.DeltaResourceStateFailed,
			Column5: true,
			Column6: []byte(fmt.Sprintf(`{"attempt": %d}`, i)),
		})
		require.NoError(t, err)
	}

	var length int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT array_length(errors, 1) FROM delta_resource WHERE id = $1", result.Resource.ID).Scan(&length))
	require.Equal(t, 20, length)
}

func TestDegradeOrphanedFailed(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{}) // not started, so jobs stay queued
	ctx := context.Background()

	orphaned, err := client.Inform(ctx, testObject{ObjectID: "orphaned"}, nil)
	require.NoError(t, err)
	retrying, err := client.Inform(ctx, testObject{ObjectID: "retrying"}, nil)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, "UPDATE delta_resource SET state = 'failed', attempted_at = NOW() - interval '2 hours'")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "DELETE FROM river_job WHERE args->>'ID' = $1::bigint::text", orphaned.Resource.ID)
	require.NoError(t, err)

	degraded, err := sqlc.New(pool).ResourceDegradeOrphanedFailed(ctx, int32(time.Hour.Seconds()))
	require.NoError(t, err)
	require.EqualValues(t, 1, degraded)
	require.Equal(t, deltatype.ResourceStateDegraded, resourceState(t, pool, orphaned.Resource.ObjectID))
	require.Equal(t, deltatype.ResourceStateFailed, resourceState(t, pool, retrying.Resource.ObjectID))
}

func TestTruncateError(t *testing.T) {
	t.Parallel()

	require.Equal(t, "short", truncateError("short"))

	long := strings.Repeat("é", maxStoredErrorLength) // 2 bytes per rune
	truncated := truncateError(long)
	require.LessOrEqual(t, len(truncated), maxStoredErrorLength)
	require.True(t, utf8.ValidString(truncated))
	require.True(t, strings.HasSuffix(truncated, "(truncated)"))
}
