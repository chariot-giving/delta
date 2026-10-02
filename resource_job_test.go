package delta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
)

func TestInform_DoesNotDuplicateQueuedJob(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{}) // not started, so jobs stay queued

	first, err := client.Inform(context.Background(), testObject{ObjectID: "dup", Value: "a"}, nil)
	require.NoError(t, err)
	_, err = client.Inform(context.Background(), testObject{ObjectID: "dup", Value: "b"}, nil)
	require.NoError(t, err)

	require.Equal(t, 1, countResourceJobs(t, pool, first.Resource.ID))
}

func TestInform_EnqueuesFollowUpWhileJobRunning(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	started := make(chan struct{})
	release := make(chan struct{})
	controller := &testController{}
	controller.workFunc = func(ctx context.Context, resource *Resource[testObject]) error {
		if resource.Object.Value == "a" {
			close(started)
			<-release
		}
		return nil
	}
	client := testClient(t, pool, controller)
	startClient(t, client)

	first, err := client.Inform(context.Background(), testObject{ObjectID: "running", Value: "a"}, nil)
	require.NoError(t, err)
	<-started

	// The change arrives while the first job is running: it must still be worked.
	informDone := make(chan error, 1)
	go func() {
		_, err := client.Inform(context.Background(), testObject{ObjectID: "running", Value: "b"}, nil)
		informDone <- err
	}()
	close(release)
	require.NoError(t, <-informDone)

	eventually(t, func() bool { return controller.WorkCalls("running") == 2 })
	eventually(t, func() bool { return resourceState(t, pool, "running") == deltatype.ResourceStateSynced })
	require.Equal(t, 2, countResourceJobs(t, pool, first.Resource.ID))
}

func TestRescueStuckScheduled_SkipsResourcesWithQueuedJob(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{}) // not started, so jobs stay queued
	ctx := context.Background()

	queued, err := client.Inform(ctx, testObject{ObjectID: "queued"}, nil)
	require.NoError(t, err)
	orphaned, err := client.Inform(ctx, testObject{ObjectID: "orphaned"}, nil)
	require.NoError(t, err)

	// Both look stuck by age; only "orphaned" has lost its River job.
	_, err = pool.Exec(ctx, "UPDATE delta_resource SET attempted_at = NOW() - interval '2 hours'")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "DELETE FROM river_job WHERE args->>'ID' = $1::bigint::text", orphaned.Resource.ID)
	require.NoError(t, err)

	rescued, err := sqlc.New(pool).ResourceRescueStuckScheduled(ctx, &sqlc.ResourceRescueStuckScheduledParams{
		Error:        []byte(`{}`),
		StuckHorizon: int32(time.Hour.Seconds()),
		Max:          100,
	})
	require.NoError(t, err)
	require.Len(t, rescued, 1)
	require.Equal(t, orphaned.Resource.ID, rescued[0].ID)
	require.Equal(t, deltatype.ResourceStateScheduled, resourceState(t, pool, queued.Resource.ObjectID))
}
