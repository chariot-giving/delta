package delta

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

func TestWorker_CancelsJobForMissingResource(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	controller := &testController{}
	client := testClient(t, pool, controller)
	startClient(t, client)

	// Insert a resource job whose resource row doesn't exist.
	_, err := client.client.Insert(context.Background(), Resource[testObject]{
		ResourceRow: &deltatypeResourceRowForTest,
		Object:      testObject{ObjectID: "missing"},
	}, &river.InsertOpts{Queue: "test_object"})
	require.NoError(t, err)

	var finalized struct {
		state  string
		errMsg string
	}
	eventually(t, func() bool {
		err := pool.QueryRow(context.Background(),
			`SELECT state::text, coalesce(errors[array_length(errors, 1)]->>'error', '')
			   FROM river_job WHERE kind = 'delta.resource.test_object'`).Scan(&finalized.state, &finalized.errMsg)
		return err == nil && finalized.state == "cancelled"
	})
	require.Contains(t, finalized.errMsg, "no longer exists")
	require.NotContains(t, finalized.errMsg, "%!w(<nil>)")
	require.Zero(t, controller.WorkCalls("missing"))
}
