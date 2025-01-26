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
)

// All known event categories, used to validate incoming categories. This is purposely not
// exported because end users should have no way of subscribing to all known
// categories for forward compatibility reasons.
var allCategories = map[EventCategory]struct{}{ //nolint:gochecknoglobals
	EventCategoryObjectSynced:  {},
	EventCategoryObjectFailed:  {},
	EventCategoryObjectDeleted: {},
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
