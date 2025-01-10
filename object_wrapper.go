package delta

import (
	"context"
	"encoding/json"

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

// Work implements Object.Work.
func (w *wrapperObject[T]) Work(ctx context.Context) error {
	return w.controller.Work(ctx, w.resource)
}
