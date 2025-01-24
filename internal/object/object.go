package object

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/deltatype"
)

// Object provides an interface to a struct that wraps a resource to be managed
// combined with a work function that can process it. Its main purpose is to
// wrap a struct that contains generic types (like a Controller[T] that needs to be
// invoked with a Resource[T]) in such a way as to make it non-generic so that it can
// be used in other non-generic code like resourceManager.
//
// Implemented by delta.wrapperObject.
type Object interface {
	UnmarshalResource() error
	Compare(other any) (int, bool)
	Work(ctx context.Context) error
	Timeout() time.Duration
	Enqueue(ctx context.Context, tx pgx.Tx, client *river.Client[pgx.Tx]) error
}

// ObjectFactory provides an interface to a struct that can generate an
// Object, a wrapper around a resource to be managed combined with a work function
// that can process it.
//
// Implemented by delta.objectFactoryWrapper.
type ObjectFactory interface {
	// Make an Object, which wraps a resource to be managed and work function that can
	// process it.
	Make(resourceRow *deltatype.ResourceRow) Object
}
