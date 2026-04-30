package delta

import (
	"time"

	"github.com/chariot-giving/delta/deltatype"
)

// Event is a record of an action that occurred on an object.
type Event struct {
	// Resource is the object for the event.
	Resource *deltatype.ResourceRow
	// EventCategory returns the type of event that occurred.
	EventCategory EventCategory
	// Timestamp is the time the event occurred.
	Timestamp time.Time
	// Drift carries additional context for EventCategoryObjectDriftDetected
	// events. It is nil for other event categories.
	Drift *DriftInfo
}

type EventCategory string

const (
	// EventCategoryObjectSynced occurs when a resource is synced.
	// Callers can use the resource object fields like `Attempt` and `CreatedAt`
	// to differentiate if the resource was created or updated.
	EventCategoryObjectSynced EventCategory = "object_synced"

	// EventCategoryObjectFailed occurs when a resource fails to sync.
	// Occurs both when a resource work fails and will be retried and when
	// a resource fails for the last time and is marked as degraded.
	// Callers can use the resource object fields like `Attempt` and `State`
	// to differentiate each type of occurrence.
	EventCategoryObjectFailed EventCategory = "object_failed"

	// EventCategoryObjectDeleted occurs when a resource is deleted.
	EventCategoryObjectDeleted EventCategory = "object_deleted"

	// EventCategoryObjectSkipped occurs when the informer observes a
	// resource whose upstream state matches Delta's stored state and is
	// already in the synced state, so no work is scheduled. Useful as a
	// liveness signal that the informer is running.
	EventCategoryObjectSkipped EventCategory = "object_skipped"

	// EventCategoryObjectDriftDetected occurs during a reconciliation pass
	// when the upstream state of a resource diverges from Delta's stored
	// state. The Drift field on the Event carries the reason and hashes.
	EventCategoryObjectDriftDetected EventCategory = "object_drift_detected"
)

// DriftReason describes why drift was detected during reconciliation.
type DriftReason string

const (
	// DriftReasonHashMismatch indicates that the upstream object's hash
	// differs from Delta's stored hash for a resource that was previously
	// in the synced state.
	DriftReasonHashMismatch DriftReason = "hash_mismatch"
	// DriftReasonNotInDelta indicates that the upstream object exists but
	// Delta has no record of it. The reconciliation pass discovered an
	// object the regular inform path missed.
	DriftReasonNotInDelta DriftReason = "not_in_delta"
)

// DriftInfo carries context about a drift event.
type DriftInfo struct {
	// Reason describes why drift was detected.
	Reason DriftReason
	// PreviousHash is the hash Delta had stored for the resource before
	// reconciliation observed drift. Empty for DriftReasonNotInDelta.
	PreviousHash []byte
	// CurrentHash is the hash Delta computed for the upstream object as
	// observed by reconciliation.
	CurrentHash []byte
}

// All known event categories, used to validate incoming categories. This is purposely not
// exported because end users should have no way of subscribing to all known
// categories for forward compatibility reasons.
var allCategories = map[EventCategory]struct{}{ //nolint:gochecknoglobals
	EventCategoryObjectSynced:        {},
	EventCategoryObjectFailed:        {},
	EventCategoryObjectDeleted:       {},
	EventCategoryObjectSkipped:       {},
	EventCategoryObjectDriftDetected: {},
}

// eventSubscription is an active subscription for events being produced by a
// client, created with Client.Subscribe.
type eventSubscription struct {
	Chan       chan Event
	Categories map[EventCategory]struct{}
}

// ListensFor returns true if the subscription listens for events of the category.
func (s *eventSubscription) ListensFor(category EventCategory) bool {
	_, ok := s.Categories[category]
	return ok
}
