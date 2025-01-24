package delta

import (
	"errors"
	"fmt"

	"github.com/riverqueue/river"

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
	var object T
	objectWrapper := &objectFactoryWrapper[T]{controller: controller}
	workConfigurer := &controllerConfigurer[T]{
		worker: &controllerWorker[T]{
			factory: objectWrapper,
		},
		informer: &controllerInformer[T]{
			factory:  objectWrapper,
			informer: controller,
		},
		scheduler: &controllerScheduler[T]{
			factory: objectWrapper,
		},
	}
	return controllers.add(object, objectWrapper, workConfigurer)
}

// Controllers is a list of available resource controllers. A Controller must be registered for
// each type of Resource to be handled.
//
// Use the top-level AddController function combined with a Controllers to register a
// controller.
type Controllers struct {
	controllerMap map[string]controllerInfo // resource kind -> controller info
}

// controllerInfo bundles information about a registered controller for later lookup
// in a Controllers bundle.
type controllerInfo struct {
	object        Object
	objectFactory object.ObjectFactory
	configurer    controllerConfigurerInterface
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

func (c Controllers) add(object Object, factory object.ObjectFactory, configurer controllerConfigurerInterface) error {
	kind := object.Kind()

	// Validate the kind before adding it to the map.
	// This is important because the kind is used as the queue name.
	if err := validateObjectKind(kind); err != nil {
		return err
	}

	if _, ok := c.controllerMap[kind]; ok {
		return fmt.Errorf("controller for kind %q is already registered", kind)
	}

	c.controllerMap[kind] = controllerInfo{
		object:        object,
		objectFactory: factory,
		configurer:    configurer,
	}

	return nil
}

func validateObjectKind(kind string) error {
	if kind == "" {
		return errors.New("kind cannot be empty")
	}
	if len(kind) > 64 {
		return errors.New("kind cannot be longer than 64 characters")
	}
	if !nameRegex.MatchString(kind) {
		return fmt.Errorf("kind is invalid, expected letters and numbers separated by underscores or hyphens: %q", kind)
	}
	return nil
}

// Non-generic interface implemented by controllerConfigurer below.
type controllerConfigurerInterface interface {
	Configure(workers *river.Workers) error
}

// Contains a generic object param to allow it to call AddWorker with a type.
type controllerConfigurer[T Object] struct {
	worker    river.Worker[Resource[T]]
	informer  river.Worker[InformArgs[T]]
	scheduler river.Worker[ScheduleArgs[T]]
}

func (cc *controllerConfigurer[T]) Configure(workers *river.Workers) error {
	if err := river.AddWorkerSafely(workers, cc.worker); err != nil {
		return err
	}
	if err := river.AddWorkerSafely(workers, cc.informer); err != nil {
		return err
	}
	if err := river.AddWorkerSafely(workers, cc.scheduler); err != nil {
		return err
	}
	return nil
}
