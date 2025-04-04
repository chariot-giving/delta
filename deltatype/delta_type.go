package deltatype

import (
	"time"
)

// Object is an interface that represents the objects for a resource of type T.
// This object is serialized into JSON and stored in the database.
//
// The struct is serialized using `encoding/json`. All exported fields are
// serialized, unless skipped with a struct field tag.
// This definition duplicates the Object interface in the delta package so that
// it can be used in other packages without creating a circular dependency.
type Object interface {
	// ID is a string that uniquely identifies the object.
	ID() string
	// Kind is a string that uniquely identifies the type of resource. This must be
	// provided on your resource object struct.
	Kind() string
}

// ResourceRow contains the properties of a resource that are persisted to the database.
// Use of `Resource[T]` will generally be preferred in user-facing code like controller
// interfaces.
type ResourceRow struct {
	// ID of the resource row. Generated as part of a Postgres sequence and generally
	// ascending in nature, but there may be gaps in it as transactions roll
	// back.
	ID int64

	// ID is the client provided ID of the resource object.
	// This is used to uniquely identify the resource and is set via `ID()` on the
	// `Object` at insertion time.
	ObjectID string

	// ObjectKind uniquely identifies the type of resource and instructs which controller
	// should work it. It is set at insertion time via `Kind()` on the
	// `Object`.
	ObjectKind string

	// Namespace is the namespace of the resource.
	// Namespaces can be configured independently and be used to isolate resources.
	// Extracted from either specific InformOpts on Inform, or InformOpts from
	// ResourceArgsWithInformOpts, or a client's default value.
	Namespace string

	// EncodedObject is the resource's Object encoded as JSON.
	EncodedObject []byte

	// Hash is a hash of the resource's Object. It is used to determine if the
	// resource has changed since the last time it was worked.
	Hash []byte

	// Metadata is a field for storing arbitrary metadata on a resource. It should
	// always be a valid JSON object payload, and users should not overwrite or
	// remove anything stored in this field by Delta.
	Metadata []byte

	// CreatedAt is when the resource record was created.
	CreatedAt time.Time

	// SyncedAt is when the resource was last synced.
	SyncedAt *time.Time

	// Attempt is the attempt number of the resource. Resources are inserted at 0, the
	// number is incremented to 1 the first time its worked, and may
	// increment further if it's either snoozed or errors.
	Attempt int

	// MaxAttempts is the maximum number of attempts to reconcile a resource before
	// it is considered degraded.
	MaxAttempts int

	// State is the state of the resource like `synced` or `pending`.
	// Resources are `unknown` when they're Delta is first informed.
	State ResourceState

	// Tags are an arbitrary list of keywords to add to the resource. They have no
	// functional behavior and are meant entirely as a user-specified construct
	// to help group and categorize resources.
	Tags []string

	// Errors is a set of errors that occurred when the resource was worked, one for
	// each attempt. Ordered from earliest error to the latest error.
	Errors []AttemptError
}

// Kind is used exclusively as a River JobArg
// This SHOULD NOT be used to determine the Object Kind.
func (r ResourceRow) Kind() string {
	return "delta.resource." + r.ObjectKind
}

type ResourceState string

const (
	// ResourceStateSynced indicates that the resource is in a synced state.
	ResourceStateSynced ResourceState = "synced"
	// ResourceStatePending indicates that the resource is in a pending state.
	// This indicates that the resource is being worked on.
	ResourceStatePending ResourceState = "pending"
	// ResourceStateExpired indicates that the resource is in an expired state.
	// This means the resource was previously synced but has not been updated in
	// a long time.
	ResourceStateExpired ResourceState = "expired"
	// ResourceStateScheduled indicates that the resource is scheduled for an operation (e.g., sync, deletion).
	ResourceStateScheduled ResourceState = "scheduled"
	// ResourceStateFailed indicates that the resource encountered an error during its lifecycle.
	ResourceStateFailed ResourceState = "failed"
	// ResourceStateDegraded indicates that the resource is operating, but not optimally.
	// This usually happens if Delta exceeds the maximum number of attempts to reconcile a resource.
	ResourceStateDegraded ResourceState = "degraded"
	// ResourceStateDeleted indicates that the resource is in a deleted state.
	ResourceStateDeleted ResourceState = "deleted"
	// ResourceStateUnknown indicates that the resource is in an unknown state.
	ResourceStateUnknown ResourceState = "unknown"
)

// ResourceStates returns all possible resource states.
func ResourceStates() []ResourceState {
	return []ResourceState{
		ResourceStateSynced,
		ResourceStatePending,
		ResourceStateExpired,
		ResourceStateScheduled,
		ResourceStateFailed,
		ResourceStateDegraded,
		ResourceStateDeleted,
		ResourceStateUnknown,
	}
}

// AttemptError is an error from a single resource attempt that failed due to an
// error or a panic.
type AttemptError struct {
	// At is the time at which the error occurred.
	At time.Time `json:"at"`

	// Attempt is the attempt number on which the error occurred (maps to
	// Attempt on a job row).
	Attempt int `json:"attempt"`

	// Error contains the stringified error of an error returned from a job or a
	// panic value in case of a panic.
	Error string `json:"error"`

	// Trace contains a stack trace from a job that panicked. The trace is
	// produced by invoking `debug.Trace()`.
	Trace string `json:"trace"`
}

// ObjectInformParams is the parameters for informing Delta of an object.
type ObjectInformParams struct {
	ResourceID    string
	Kind          string
	Namespace     string
	CreatedAt     *time.Time
	EncodedObject []byte
	Metadata      []byte
	State         ResourceState
	Tags          []string
	Hash          []byte
}

type ObjectInformResult struct {
	// Resource is the resource that was informed.
	Resource *ResourceRow

	// AlreadyExists is true if the resource already existed in the database.
	AlreadyExists bool
}

type Namespace struct {
	// Name is the name of the space.
	Name string
	// CreatedAt is the time at which the queue first began being worked by a
	// client. Unused queues are deleted after a retention period, so this only
	// reflects the most recent time the queue was created if there was a long
	// gap.
	CreatedAt time.Time
	// Metadata is a field for storing arbitrary metadata on a queue. It is
	// currently reserved for River's internal use and should not be modified by
	// users.
	Metadata []byte
	// UpdatedAt is the last time the queue was updated. This field is updated
	// periodically any time an active Client is configured to work the queue,
	// even if the queue is paused.
	//
	// If UpdatedAt has not been updated for awhile, the queue record will be
	// deleted from the table by a maintenance process.
	UpdatedAt time.Time
	// ExpiryDuration is the time after which resources within the namespace
	// should be considered expired and requeued for work.
	// If this is nil, resources will never expire.
	ExpiryDuration *time.Duration
}
