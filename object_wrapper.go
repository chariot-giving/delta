package delta

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/object"
)

// objectFactoryWrapper wraps a Worker to implement objectFactory.
type objectFactoryWrapper[T Object] struct {
	controller Controller[T]
}

// Make implements ObjectFactory.Make.
func (w *objectFactoryWrapper[T]) Make(resourceRow *deltatype.ResourceRow) object.Object {
	return &wrapperObject[T]{
		resourceRow: resourceRow,
		controller:  w.controller,
	}
}

// wrapperWorkUnit implements workUnit for a job and Worker.
type wrapperObject[T Object] struct {
	resource    *Resource[T] // not set until after UnmarshalResource is invoked
	resourceRow *deltatype.ResourceRow
	controller  Controller[T]
}

// UnmarshalResource implements Object.UnmarshalResource.
func (w *wrapperObject[T]) UnmarshalResource() error {
	w.resource = &Resource[T]{
		ResourceRow: w.resourceRow,
	}

	return json.Unmarshal(w.resource.EncodedObject, &w.resource.Object)
}

// Compare implements Object.Compare.
func (w *wrapperObject[T]) Compare(other any) (int, bool) {
	if comparableObj, ok := other.(ComparableObject); ok {
		if reflect.TypeOf(other) == reflect.TypeOf(w.resource.Object) {
			return comparableObj.Compare(w.resource.Object), true
		}
	}

	return 0, false
}

// Work implements Object.Work.
func (w *wrapperObject[T]) Work(ctx context.Context) error {
	return w.controller.Work(ctx, w.resource)
}

// Timeout implements Object.Timeout.
func (w *wrapperObject[T]) Timeout() time.Duration {
	return w.controller.ResourceTimeout(w.resource)
}

// Enqueue implements Object.Enqueue.
func (w *wrapperObject[T]) Enqueue(ctx context.Context, tx pgx.Tx, client *river.Client[pgx.Tx]) error {
	if w.resource == nil {
		return errors.New("resource is nil; UnmarshalResource must be called first")
	}

	resource := *w.resource

	insertOpts := &river.InsertOpts{
		Queue:    resource.ObjectKind, // this ensures the job will be picked up by a client who is configured with this controller
		Tags:     resource.Tags,
		Metadata: resource.Metadata,
	}

	if tx != nil {
		if _, err := client.InsertTx(ctx, tx, resource, insertOpts); err != nil {
			return err
		}
		return nil
	}

	if _, err := client.Insert(ctx, resource, insertOpts); err != nil {
		return err
	}
	return nil
}
