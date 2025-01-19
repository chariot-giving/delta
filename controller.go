package delta

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/object"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
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
			pool:    controllers.pool,
			factory: objectWrapper,
		},
		informer: &controllerInformer[T]{
			pool:     controllers.pool,
			factory:  objectWrapper,
			informer: controller,
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
	pool          *pgxpool.Pool
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
func NewControllers(pool *pgxpool.Pool) *Controllers {
	return &Controllers{
		controllerMap: make(map[string]controllerInfo),
		pool:          pool,
	}
}

func (c Controllers) add(object Object, factory object.ObjectFactory, configurer controllerConfigurerInterface) error {
	kind := object.Kind()

	if _, ok := c.controllerMap[kind]; ok {
		return fmt.Errorf("controller for kind %q is already registered", kind)
	}

	c.controllerMap[kind] = controllerInfo{
		object:        object,
		objectFactory: factory,
		configurer:    configurer,
	}

	// seed the initial controller inform
	// TODO: update this code based on service restarts
	queries := sqlc.New(c.pool)
	_, err := queries.ControllerInformCreate(context.Background(), &sqlc.ControllerInformCreateParams{
		ResourceKind:    kind,
		Opts:            json.RawMessage(`{}`),
		Metadata:        json.RawMessage(`{}`),
		ProcessExisting: false,
		RunForeground:   false,
	})
	if err != nil {
		return err
	}

	return nil
}

// Non-generic interface implemented by controllerConfigurer below.
type controllerConfigurerInterface interface {
	Configure(workers *river.Workers)
}

// Contains a generic object param to allow it to call AddWorker with a type
type controllerConfigurer[T Object] struct {
	worker   river.Worker[Resource[T]]
	informer river.Worker[InformArgs[T]]
}

func (cc *controllerConfigurer[T]) Configure(workers *river.Workers) {
	river.AddWorker(workers, cc.worker)
	river.AddWorker(workers, cc.informer)
}
