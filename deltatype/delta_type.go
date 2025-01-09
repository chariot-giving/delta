package deltatype

import "time"

type ResourceRow struct {
	// ID of the resource. Generated as part of a Postgres sequence and generally
	// ascending in nature, but there may be gaps in it as transactions roll
	// back.
	ID int64

	// Kind uniquely identifies the type of resource and instructs which worker
	// should work it. It is set at insertion time via `Kind()` on the
	// `Object`.
	Kind string

	// EncodedObject is the resource's Object encoded as JSON.
	EncodedObject []byte

	// Metadata is a field for storing arbitrary metadata on a resource. It should
	// always be a valid JSON object payload, and users should not overwrite or
	// remove anything stored in this field by Delta.
	Metadata []byte

	// CreatedAt is when the resource record was created.
	CreatedAt time.Time

	// SyncedAt is when the resource was last synced.
	SyncedAt *time.Time

	// Annotations are an arbitrary list of keywords to add to the resource. They have no
	// functional behavior and are meant entirely as a user-specified construct
	// to help group and categorize resources.
	Annotations []string
}

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
