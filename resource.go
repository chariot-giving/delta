package delta

import "github.com/chariot-giving/delta/deltatype"

// Resource represents a single object, holding both the object and
// information for a resource with object of type T.
type Resource[T Object] struct {
	*deltatype.ResourceRow

	// Object is the object for the resource.
	Object T
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

// ComparableObject is an interface that combines Object with a method for comparison.
type ComparableObject interface {
	Object
	Compare(other Object) int
}
