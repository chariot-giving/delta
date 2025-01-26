package delta

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// The default maximum size of the subscribe channel. Events that would overflow
// it will be dropped.
const subscribeChanSizeDefault = 1_000

// SubscribeConfig is more thorough subscription configuration used for
// Client.SubscribeConfig.
type SubscribeConfig struct {
	// ChanSize is the size of the buffered channel that will be created for the
	// subscription. Incoming events that overall this number because a listener
	// isn't reading from the channel in a timely manner will be dropped.
	//
	// Defaults to 1000.
	ChanSize int

	// Categories are the category of object events that the subscription will receive.
	// Requiring that categories are specified explicitly allows for forward
	// compatibility in case new kinds of events are added in future versions.
	// If new event categories are added, callers will have to explicitly add them to
	// their requested list and ensure they can be handled correctly.
	Categories []EventCategory
}

type subscriptionManager struct {
	logger  *slog.Logger
	eventCh <-chan []Event

	mu               sync.Mutex // protects subscription fields
	subscriptions    map[int]*eventSubscription
	subscriptionsSeq int // used for generating simple IDs
}

// ResetEventChan is used to change the channel that the subscription
// manager listens on. It must only be called when the subscription manager is
// stopped.
func (sm *subscriptionManager) ResetEventChan(eventCh <-chan []Event) {
	sm.eventCh = eventCh
}

// Start starts the subscription manager. It will block until the context is canceled.
// It is expected that the client will call this in a goroutine.
func (sm *subscriptionManager) Start(ctx context.Context) {
	sm.logger.DebugContext(ctx, "SubscriptionManager: Run loop started")
	defer sm.logger.DebugContext(ctx, "SubscriptionManager: Run loop stopped")

	// On shutdown, close and remove all active subscriptions.
	defer func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()

		for subID, sub := range sm.subscriptions {
			close(sub.Chan)
			delete(sm.subscriptions, subID)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Distribute remaining subscriptions until the channel is
			// closed. This does make the subscription manager a little
			// problematic in that it requires the subscription channel to
			// be closed before it will fully stop. This always happens in
			// the case of a real client by virtue of the completer always
			// stopping at the same time as the subscription manager, but
			// one has to be careful in tests.
			sm.logger.DebugContext(ctx, "SubscriptionManager: Stopping; distributing subscriptions until channel is closed")
			for events := range sm.eventCh {
				sm.distributeEvents(events)
			}

			return
		case events := <-sm.eventCh:
			sm.distributeEvents(events)
		}
	}
}

// Special internal variant that lets us inject an overridden size.
func (sm *subscriptionManager) SubscribeConfig(config *SubscribeConfig) (<-chan Event, func()) {
	if config.ChanSize < 0 {
		panic("SubscribeConfig.ChanSize must be greater or equal to 1")
	}
	if config.ChanSize == 0 {
		config.ChanSize = subscribeChanSizeDefault
	}

	for _, kind := range config.Categories {
		if _, ok := allCategories[kind]; !ok {
			panic(fmt.Errorf("unknown event category: %s", kind))
		}
	}

	subChan := make(chan Event, config.ChanSize)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Just gives us an easy way of removing the subscription again later.
	subID := sm.subscriptionsSeq
	sm.subscriptionsSeq++

	sm.subscriptions[subID] = &eventSubscription{
		Chan:       subChan,
		Categories: keyBy(config.Categories, func(k EventCategory) (EventCategory, struct{}) { return k, struct{}{} }),
	}

	cancel := func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()

		// May no longer be present in case this was called after a stop.
		sub, ok := sm.subscriptions[subID]
		if !ok {
			return
		}

		close(sub.Chan)

		delete(sm.subscriptions, subID)
	}

	return subChan, cancel
}

// Receives events from Delta event channel and distributes events into
// any listening subscriber channels.
// (Subscriber channels are non-blocking so this should be quite fast.)
func (sm *subscriptionManager) distributeEvents(events []Event) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Quick path so we don't need to allocate anything if no one is listening.
	if len(sm.subscriptions) < 1 {
		return
	}

	for _, event := range events {
		sm.distributeObjectEvent(event)
	}
}

// Distribute a single event into any listening subscriber channels.
//
// MUST be called with sm.mu already held.
func (sm *subscriptionManager) distributeObjectEvent(event Event) {
	// All subscription channels are non-blocking so this is always fast and
	// there's no risk of falling behind what producers are sending.
	for _, sub := range sm.subscriptions {
		if sub.ListensFor(event.EventCategory) {
			select {
			case sub.Chan <- event:
			default:
			}
		}
	}
}

// KeyBy converts a slice into a map using the key/value tuples returned by
// tupleFunc. If any two pairs would have the same key, the last one wins. Go
// maps are unordered and the order of the new map isn't guaranteed to the same
// as the original slice.
func keyBy[T any, K comparable, V any](collection []T, tupleFunc func(item T) (K, V)) map[K]V {
	result := make(map[K]V, len(collection))

	for _, t := range collection {
		k, v := tupleFunc(t)
		result[k] = v
	}

	return result
}
