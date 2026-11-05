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
