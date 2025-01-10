package delta

import (
	"fmt"

	"github.com/chariot-giving/delta/internal/object"
)

// Controller is an interface that can manage a resource with object of type T.
type Controller[T Object] interface {
	Informer[T]
	Worker[T]
}

// AddController registers a Controller on the provided Controllers bundle. Each Controller must
// be registered so that the Client knows it should handle a specific kind of
// resource (as returned by its `Kind()` method).
//
// Use by explicitly specifying a Object type and then passing an instance of a
// controller for the same type:
//
//	delta.AddController(controllers, &UserController{})
//
// Note that AddController can panic in some situations, such as if the controller is
// already registered or if its configuration is otherwise invalid. This default
// probably makes sense for most applications because you wouldn't want to start
// an application with invalid hardcoded runtime configuration. If you want to
// avoid panics, use AddControllerSafely instead.
func AddController[T Object](controllers *Controllers, controller Controller[T]) {
	if err := AddControllerSafely[T](controllers, controller); err != nil {
		panic(err)
	}
}

// AddControllerSafely registers a controller on the provided Controllers bundle. Unlike AddController,
// AddControllerSafely does not panic and instead returns an error if the controller
// is already registered or if its configuration is invalid.
//
// Use by explicitly specifying a Object type and then passing an instance of a
// controller for the same type:
//
//	delta.AddControllerSafely[User](controllers, &UserController{}).
func AddControllerSafely[T Object](controllers *Controllers, controller Controller[T]) error {
	var jobArgs T
	return controllers.add(jobArgs, &objectFactoryWrapper[T]{controller: controller})
}

// Controllers is a list of available resource controllers. A Controller must be registered for
// each type of Resource to be handled.
//
// Use the top-level AddController function combined with a Controllers to register a
// controller.
type Controllers struct {
	controllerMap map[string]controllerInfo // job kind -> controller info
}

// controllerInfo bundles information about a registered controller for later lookup
// in a Controllers bundle.
type controllerInfo struct {
	object        Object
	objectFactory object.ObjectFactory
}

// NewControllers initializes a new registry of available resource controllers.
//
// Use the top-level AddController function combined with a Controllers registry to
// register each available controller.
func NewControllers() *Controllers {
	return &Controllers{
		controllerMap: make(map[string]controllerInfo),
	}
}

func (w Controllers) add(object Object, objectFactory object.ObjectFactory) error {
	kind := object.Kind()

	if _, ok := w.controllerMap[kind]; ok {
		return fmt.Errorf("controller for kind %q is already registered", kind)
	}

	w.controllerMap[kind] = controllerInfo{
		object:        object,
		objectFactory: objectFactory,
	}

	return nil
}
