package delta

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	controllerWorker := &controllerWorker[T]{
		pool:    controllers.pool,
		factory: objectWrapper,
	}
	controllerInformer := &controllerInformer[T]{
		pool:       controllers.pool,
		factory:    objectWrapper,
		controller: controller,
	}
	workConfigurer := &workConfigurer[T]{
		worker:   controllerWorker,
		informer: controllerInformer,
	}
	return controllers.add(object, workConfigurer)
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
	object         Object
	workConfigurer workConfigurerInterface
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

func (w Controllers) add(object Object, workConfigurer workConfigurerInterface) error {
	kind := object.Kind()

	if _, ok := w.controllerMap[kind]; ok {
		return fmt.Errorf("controller for kind %q is already registered", kind)
	}

	w.controllerMap[kind] = controllerInfo{
		object:         object,
		workConfigurer: workConfigurer,
	}

	// seed the initial controller inform
	// TODO: update this code based on service restarts
	queries := sqlc.New(w.pool)
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

type controllerWorker[T Object] struct {
	pool    *pgxpool.Pool
	factory object.ObjectFactory
	river.WorkerDefaults[Resource[T]]
}

func (w *controllerWorker[T]) Work(ctx context.Context, job *river.Job[Resource[T]]) error {
	queries := sqlc.New(w.pool)
	resource := job.Args
	object := w.factory.Make(resource.ResourceRow)
	if err := object.UnmarshalResource(); err != nil {
		return fmt.Errorf("failed to unmarshal resource: %w", err)
	}

	// do the work!
	err := object.Work(ctx)
	if err != nil {
		state := sqlc.DeltaResourceStateFailed
		if job.Attempt >= job.MaxAttempts {
			state = sqlc.DeltaResourceStateDegraded
		}
		_, uerr := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
			ID:      resource.ID,
			Column1: true,
			State:   state,
			Column3: false,
			Column5: true,
			Column6: []byte(err.Error()),
		})
		if uerr != nil {
			return uerr
		}
		return err
	}

	now := time.Now()
	_, err = queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:       resource.ID,
		Column1:  true,
		State:    sqlc.DeltaResourceStateSynced,
		Column3:  true,
		SyncedAt: &now,
		Column5:  false,
	})
	if err != nil {
		return err
	}

	return nil
}
