package delta

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/deltatype"
)

func newTestSubscriptionManager(metrics MetricsCollector) *subscriptionManager {
	return &subscriptionManager{
		logger:        slog.New(slog.DiscardHandler),
		metrics:       metrics,
		subscriptions: make(map[int]*eventSubscription),
		mu:            sync.Mutex{},
	}
}

func TestSubscriptionManager_DropsAreCounted(t *testing.T) {
	t.Parallel()

	rec := &recordingMetrics{}
	manager := newTestSubscriptionManager(rec)

	// Subscribe with a channel of size 1 so we can deliberately overflow it.
	subCh, cancel := manager.SubscribeConfig(&SubscribeConfig{
		Categories: []EventCategory{EventCategoryObjectSynced},
		ChanSize:   1,
	})
	defer cancel()

	manager.mu.Lock()
	manager.distributeObjectEvent(context.Background(), Event{
		Resource:      &deltatype.ResourceRow{ObjectKind: "k", ObjectID: "1"},
		EventCategory: EventCategoryObjectSynced,
		Timestamp:     time.Now(),
	})
	manager.distributeObjectEvent(context.Background(), Event{
		Resource:      &deltatype.ResourceRow{ObjectKind: "k", ObjectID: "2"},
		EventCategory: EventCategoryObjectSynced,
		Timestamp:     time.Now(),
	})
	manager.mu.Unlock()

	// First event should be sitting in the buffer; second should have been dropped.
	dropped := rec.countersByName(MetricSubscriptionDropped)
	require.Len(t, dropped, 1, "expected exactly one dropped-event counter increment")
	require.Equal(t, string(EventCategoryObjectSynced), dropped[0].labels["category"])

	// And the buffered event should still be readable.
	select {
	case ev := <-subCh:
		require.Equal(t, EventCategoryObjectSynced, ev.EventCategory)
		require.Equal(t, "1", ev.Resource.ObjectID)
	case <-time.After(time.Second):
		t.Fatal("expected an event on the subscription channel")
	}
}

func TestSubscriptionManager_NoSubscribersIsCheap(t *testing.T) {
	t.Parallel()

	rec := &recordingMetrics{}
	manager := newTestSubscriptionManager(rec)

	// distributeEvents on an empty subscription map must short-circuit
	// without recording any drops.
	manager.distributeEvents(context.Background(), []Event{
		{EventCategory: EventCategoryObjectSynced, Timestamp: time.Now()},
	})
	require.Empty(t, rec.countersByName(MetricSubscriptionDropped))
}

func TestSubscribeConfig_RejectsUnknownCategory(t *testing.T) {
	t.Parallel()

	manager := newTestSubscriptionManager(noopMetrics{})

	require.PanicsWithError(t, "unknown event category: nope",
		func() {
			_, _ = manager.SubscribeConfig(&SubscribeConfig{Categories: []EventCategory{"nope"}})
		},
	)
}

func TestSubscribe_SkippedCategoryIsAccepted(t *testing.T) {
	t.Parallel()

	manager := newTestSubscriptionManager(noopMetrics{})

	// EventCategoryObjectSkipped is new in this change — the registry must
	// accept it, otherwise callers can't subscribe to it.
	_, cancel := manager.SubscribeConfig(&SubscribeConfig{
		Categories: []EventCategory{EventCategoryObjectSkipped},
	})
	cancel()
}
