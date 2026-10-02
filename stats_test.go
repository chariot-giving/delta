package delta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/deltatype"
)

func TestClient_StatsAndEnqueueMetrics(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	metrics := &recordingMetrics{}
	client := testClient(t, pool, &testController{}, func(c *Config) { c.Metrics = metrics }) // not started
	ctx := context.Background()

	_, err := client.Inform(ctx, testObject{ObjectID: "stats", Value: "a"}, nil)
	require.NoError(t, err)
	_, err = client.Inform(ctx, testObject{ObjectID: "stats", Value: "b"}, nil)
	require.NoError(t, err)

	results := map[string]int{}
	for _, m := range metrics.countersByName(MetricResourceEnqueued) {
		results[m.labels["result"]]++
	}
	require.Equal(t, map[string]int{EnqueueResultInserted: 1, EnqueueResultDeduplicated: 1}, results)

	informs := map[string]int{}
	for _, m := range metrics.countersByName(MetricClientInforms) {
		informs[m.labels["result"]]++
	}
	require.Equal(t, map[string]int{ObjectResultCreated: 1, ObjectResultUpdated: 1}, informs)

	stats, err := client.Stats(ctx)
	require.NoError(t, err)
	require.Len(t, stats.Resources, 1)
	require.Equal(t, ResourceStat{Kind: "test_object", State: deltatype.ResourceStateScheduled, Count: 1}, withoutAge(stats.Resources[0]))
	require.Len(t, stats.Jobs, 1)
	require.Equal(t, "test_object", stats.Jobs[0].Queue)
	require.Equal(t, "delta.resource.test_object", stats.Jobs[0].Kind)
	require.Equal(t, "available", stats.Jobs[0].State)
	require.EqualValues(t, 1, stats.Jobs[0].Count)
	require.GreaterOrEqual(t, stats.Jobs[0].OldestScheduledAge, time.Duration(0))
}

func withoutAge(stat ResourceStat) ResourceStat {
	stat.OldestAge = 0
	return stat
}

func TestClient_StopClosesSubscriptions(t *testing.T) {
	t.Parallel()

	pool := testPool(t)
	client := testClient(t, pool, &testController{})
	require.NoError(t, client.Start(context.Background()))

	events, cancel := client.Subscribe(EventCategoryObjectSynced)
	defer cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	require.NoError(t, client.Stop(stopCtx))

	select {
	case _, ok := <-events:
		require.False(t, ok, "expected subscription channel to be closed")
	case <-time.After(5 * time.Second):
		t.Fatal("subscription channel was not closed after Stop")
	}
}
