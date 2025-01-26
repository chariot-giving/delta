package delta

import (
	"time"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
)

// Resource represents a single object, holding both the object and
// information for a resource with object of type T.
type Resource[T Object] struct {
	*deltatype.ResourceRow

	// Object is the object for the resource.
	Object T
}

// Kind is specific to the River package and NOT meant to be used as the Resource/Object Kind.
func (r Resource[T]) Kind() string {
	return "delta.resource." + r.Object.Kind()
}

// Object is an interface that represents the objects for a resource of type T.
// This object is serialized into JSON and stored in the database.
//
// The struct is serialized using `encoding/json`. All exported fields are
// serialized, unless skipped with a struct field tag.
type Object interface {
	// ID is a string that uniquely identifies the object.
	ID() string
	// Kind is a string that uniquely identifies the type of resource. This must be
	// provided on your resource object struct.
	Kind() string
}

// ObjectWithInformArgs is an extra interface that a resource may implement on top
// of Object to provide inform-time options for all resources of this type.
type ObjectWithInformOpts interface {
	// InformOpts returns options for all resources of this job type, overriding any
	// system defaults. These can also be overridden at inform time.
	InformOpts() InformOpts
}

// ObjectWithSettings is an extra interface that a resource may implement on top
// of Object to provide settings for all resources of this type.
type ObjectWithSettings interface {
	// Behavior returns the settings for this object.
	Settings() ObjectSettings
}

type ObjectSettings struct {
	// Parallelism is the number of concurrent workers that can be run for this
	// kind of resource. If this is 0, the default parallelism of 1 is used.
	//
	// Must be a zero or positive integer less than or equal to 10,000.
	Parallelism int

	// InformInterval is the interval at which the informer should run for this
	// kind of resource. If this is 0, the client's config inform interval is used.
	//
	// If < 0, the informer is disabled.
	InformInterval time.Duration
}

// ComparableObject is an interface that combines Object with a method for comparison.
type ComparableObject interface {
	Object
	Compare(other Object) int
}

func toResourceRow(resource *sqlc.DeltaResource) deltatype.ResourceRow {
	return deltatype.ResourceRow{
		ID:            resource.ID,
		ObjectID:      resource.ObjectID,
		ObjectKind:    resource.Kind,
		Namespace:     resource.Namespace,
		EncodedObject: resource.Object,
		Hash:          resource.Hash,
		Metadata:      resource.Metadata,
		CreatedAt:     resource.CreatedAt,
		SyncedAt:      resource.SyncedAt,
		Attempt:       int(resource.Attempt),
		MaxAttempts:   int(resource.MaxAttempts),
		State:         deltatype.ResourceState(resource.State),
		Tags:          resource.Tags,
	}
}
