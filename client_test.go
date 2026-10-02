package delta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClient_InformSyncsResource(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	controller := &testController{}
	client := testClient(t, pool, controller)
	startClient(t, client)

	result, err := client.Inform(context.Background(), testObject{ObjectID: "obj-1", Value: "a"}, &InformOpts{})
	require.NoError(t, err)
	require.False(t, result.AlreadyExists)

	eventually(t, func() bool { return resourceState(t, pool, "obj-1") == "synced" })
	require.Equal(t, 1, controller.WorkCalls("obj-1"))
}

func TestClient_InformNilOpts(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	controller := &testController{}
	client := testClient(t, pool, controller)
	startClient(t, client)

	_, err := client.Inform(context.Background(), testObject{ObjectID: "obj-nil-opts"}, nil)
	require.NoError(t, err)

	eventually(t, func() bool { return resourceState(t, pool, "obj-nil-opts") == "synced" })
}

func TestClient_ScheduleInformNilOptions(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	controller := &testController{informObjects: []testObject{{ObjectID: "informed-1"}, {ObjectID: "informed-2"}}}
	client := testClient(t, pool, controller)
	startClient(t, client)

	err := client.ScheduleInform(context.Background(), ScheduleInformParams{ResourceKind: "test_object"}, nil)
	require.NoError(t, err)

	eventually(t, func() bool {
		var synced int
		require.NoError(t, pool.QueryRow(context.Background(),
			"SELECT count(*) FROM delta_resource WHERE state = 'synced'").Scan(&synced))
		return synced == 2
	})
}
