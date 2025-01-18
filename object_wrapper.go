package delta

import (
	"context"
	"encoding/json"
	"reflect"

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
	if comparable, ok := other.(ComparableObject); ok {
		if reflect.TypeOf(other) == reflect.TypeOf(w.resource.Object) {
			return comparable.Compare(w.resource.Object), true
		}
	}

	return 0, false
}

// Work implements Object.Work.
func (w *wrapperObject[T]) Work(ctx context.Context) error {
	return w.controller.Work(ctx, w.resource)
}
