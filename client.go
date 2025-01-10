package delta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/chariot-giving/delta/deltacommon"
	"github.com/chariot-giving/delta/deltadriver"
	"github.com/chariot-giving/delta/deltaqueue"
	"github.com/chariot-giving/delta/deltashared/util/valutil"
	"github.com/chariot-giving/delta/deltatype"
	"github.com/riverqueue/river/rivershared/baseservice"
	"github.com/riverqueue/river/rivershared/startstop"
	"github.com/riverqueue/river/rivertype"
)

// Config is the configuration for a Client.
type Config struct {
	// ID is the unique identifier for this client. If not set, a random
	// identifier will be generated.
	//
	// This is used to identify the client in job attempts and for leader election.
	// This value must be unique across all clients in the same database and
	// schema and there must not be more than one process running with the same
	// ID at the same time.
	//
	// A client ID should differ between different programs and must be unique
	// across all clients in the same database and schema. There must not be
	// more than one process running with the same ID at the same time.
	// Duplicate IDs between processes will lead to facilities like leader
	// election or client statistics to fail in novel ways. However, the client
	// ID is shared by all executors within any given client. (i.e.  different
	// Go processes have different IDs, but IDs are shared within any given
	// process.)
	//
	// If in doubt, leave this property empty.
	ID string

	// Logger is the structured logger to use for logging purposes. If none is
	// specified, logs will be emitted to STDOUT with messages at warn level
	// or higher.
	Logger *slog.Logger

	// Controllers is a bundle of registered resource controllers.
	//
	// This field may be omitted for a program that's only informing resources
	// rather than working them, but if it is configured the client can validate
	// ahead of time that a controller is properly registered for an inserted resource.
	// (i.e.  That it wasn't forgotten by accident.)
	Controllers *Controllers

	// Namespaces is a list of namespaces for this client to operate on along
	// with configuration for each namespace.
	//
	// This field may be omitted for a program that's only informing resources rather
	// than managing them. If it's specified, then Controllers must also be given.
	Namespaces map[string]NamespaceConfig
}

// Indicates whether with the given configuration, this client will be expected
// to manage resources (rather than just being used to inform them). Managing resources
// requires a set of configured namespaces.
func (c *Config) willManageResources() bool {
	return len(c.Namespaces) > 0
}

// NamespaceConfig contains namespace-specific configuration.
type NamespaceConfig struct {
	// MaxWorkers is the maximum number of workers to run for the namespace, or put
	// otherwise, the maximum parallelism to run.
	//
	// This is the maximum number of workers within this particular client
	// instance, but note that it doesn't control the total number of workers
	// across parallel processes. Installations will want to calculate their
	// total number by multiplying this number by the number of parallel nodes
	// running River clients configured to the same database and queue.
	//
	// Requires a minimum of 1, and a maximum of 10,000.
	MaxWorkers int
	// ExpiryDuration is the duration after which a resource is considered expired.
	//
	// If this is set to nil, resources within the namespace will never expire.
	ResourceExpiry *time.Duration
}

// Client is a single isolated instance of Delta. Your application may use
// multiple instances operating on different databases or Postgres schemas
// within a single database.
type Client[TTx any] struct {
	config     *Config
	driver     deltadriver.Driver[TTx]
	jobQueue   deltaqueue.Queue[TTx]
	namespaces *NamespaceBundle
	// TODO: add namespaces and namespace maintainer
	stopped <-chan struct{}
	// workCancel cancels the context used for all work goroutines. Normal Stop
	// does not cancel that context.
	workCancel context.CancelCauseFunc
}

var (
	errMissingConfig                     = errors.New("missing config")
	errMissingDatabasePoolWithNamespaces = errors.New("must have a non-nil database pool to control resources (either use a driver with database pool or don't configure Controllers)")
	errMissingDriver                     = errors.New("missing database driver (try wrapping a Pgx pool with delta/deltadriver/deltapgxv5.New)")
)

// NewClient creates a new Delta client with the provided configuration.
func NewClient[TTx any](driver deltadriver.Driver[TTx], queue deltaqueue.Queue[TTx], config *Config) (*Client[TTx], error) {
	if driver == nil {
		return nil, errMissingDriver
	}
	if config == nil {
		return nil, errMissingConfig
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		}))
	}

	client := &Client[TTx]{
		config:     config,
		driver:     driver,
		jobQueue:   queue,
		namespaces: &NamespaceBundle{},
		workCancel: func(cause error) {}, // replaced on start, but here in case StopAndCancel is called before start up
	}

	for namespace, namespaceConfig := range config.Namespaces {
		client.namespaces.addManager(namespace, namespaceConfig)
	}

	if config.willManageResources() {
		if !driver.HasPool() {
			return nil, errMissingDatabasePoolWithNamespaces
		}

		// add controllers as workers of the jobqueue
		if config.Controllers != nil {
			for _, controller := range config.Controllers.controllerMap {
				queue.AddWorker(controller.worker)
			}
		}
	}

	return client, nil
}

// Start starts the client.
func (c *Client[TTx]) Start(ctx context.Context) error {

	// Startup code. Wrapped in a closure so it doesn't have to remember to
	// close the stopped channel if returning with an error.
	if err := func() error {
		if !c.config.willManageResources() {
			return errors.New("client Namespaces and Controllers must be configured for a client to start working")
		}
		if c.config.Controllers != nil && len(c.config.Controllers.controllerMap) < 1 {
			return errors.New("at least one Controller must be added to the Controller bundle")
		}

		// Before doing anything else, make an initial connection to the database to
		// verify that it appears healthy. Many of the subcomponents below start up
		// in a goroutine and in case of initial failure, only produce a log line,
		// so even in the case of a fundamental failure like the database not being
		// available, the client appears to have started even though it's completely
		// non-functional. Here we try to make an initial assessment of health and
		// return quickly in case of an apparent problem.
		_, err := c.driver.GetExecutor().Exec(ctx, "SELECT 1")
		if err != nil {
			return fmt.Errorf("error making initial connection to database: %w", err)
		}

		if err = c.jobQueue.Start(ctx); err != nil {
			return fmt.Errorf("error starting job queue: %w", err)
		}

		// We use separate contexts for fetching and working to allow for a graceful
		// stop. Both inherit from the provided context, so if it's cancelled, a
		// more aggressive stop will be initiated.
		workCtx, workCancel := context.WithCancelCause(withClient[TTx](ctx, c))

		c.workCancel = workCancel

		return nil
	}(); err != nil {
		defer stopped()
		if errors.Is(context.Cause(ctx), startstop.ErrStop) {
			return deltacommon.ErrShutdown
		}
		return err
	}

	return nil
}

// Stop stops the client.
func (c *Client[TTx]) Stop(ctx context.Context) error {
	c.workCancel(deltacommon.ErrShutdown)

	err := c.jobQueue.Stop(ctx)
	if err != nil {
		return fmt.Errorf("error stopping job queue: %w", err)
	}

	return nil
}

// Stopped returns a channel that will be closed when the Client has stopped.
// It can be used to wait for a graceful shutdown to complete.
//
// It is not affected by any contexts passed to Stop or StopAndCancel.
func (c *Client[TTx]) Stopped() <-chan struct{} {
	return c.stopped
}

// Namespaces returns the currently configured set of namespaces for this client,
// can can be used to add new ones.
func (c *Client[TTx]) Namespaces() *NamespaceBundle {
	return c.namespaces
}

var errNoDriverDBPool = errors.New("driver must have non-nil database pool to use non-transactional methods like Inform and InformMany (try InformTx or InformManyTx instead")

// Inform informs the Delta Controller about a resource object.
func (c *Client[TTx]) Inform(ctx context.Context, object Object, opts *InformOpts) (*deltatype.ObjectInformResult, error) {
	if !c.driver.HasPool() {
		return nil, errNoDriverDBPool
	}

	tx, err := c.driver.GetExecutor().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	informed, err := c.inform(ctx, tx, object, opts)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return informed, nil
}

func (c *Client[TTx]) inform(ctx context.Context, tx TTx, object Object, opts *InformOpts) (*deltatype.ObjectInformResult, error) {
	params := []InformManyParams{{Object: object, InformOpts: opts}}
	results, err := c.validateParamsAndInformMany(ctx, tx, params)
	if err != nil {
		return nil, err
	}

	return results[0], nil
}

// InformManyParams encapsulates a single resource object combined with options for
// use with batch insertion.
type InformManyParams struct {
	// Object represents the resource to inform.
	Object Object

	// InformOpts are inform options for this job.
	InformOpts *InformOpts
}

// validateParamsAndInformMany is a helper method that wraps the informMany
// method to provide param validation and conversion prior to calling the actual
// informMany method.
func (c *Client[TTx]) validateParamsAndInformMany(ctx context.Context, tx deltadriver.ExecutorTx, params []InformManyParams) ([]*deltatype.ObjectInformResult, error) {
	informParams, err := c.informManyParams(params)
	if err != nil {
		return nil, err
	}

	return c.informMany(ctx, tx, informParams)
}

// Validates input parameters for a batch inform operation and generates a set
// of batch inform parameters.
func (c *Client[TTx]) informManyParams(params []InformManyParams) ([]*deltatype.ObjectInformParams, error) {
	if len(params) < 1 {
		return nil, errors.New("no resources to inform")
	}

	informParams := make([]*deltatype.ObjectInformParams, len(params))
	for i, param := range params {
		if err := c.validateObject(param.Object); err != nil {
			return nil, err
		}

		informParamsItem, err := insertParamsFromConfigArgsAndOptions(&c.baseService.Archetype, c.config, param.Object, param.InformOpts)
		if err != nil {
			return nil, err
		}

		informParams[i] = informParamsItem
	}

	return informParams, nil
}

// Validates object prior to informing. Currently, verifies that a controller to
// handle the kind is registered in the configured controllers bundle. An
// inform-only client doesn't require a controllers bundle be configured though, so
// no validation occurs if one wasn't.
func (c *Client[TTx]) validateObject(object Object) error {
	if c.config.Controllers == nil {
		return nil
	}

	if _, ok := c.config.Controllers.controllerMap[object.Kind()]; !ok {
		return &UnknownResourceKindError{Kind: object.Kind()}
	}

	return nil
}

func insertParamsFromConfigArgsAndOptions(archetype *baseservice.Archetype, config *Config, object Object, informOpts *InformOpts) (*deltatype.ObjectInformParams, error) {
	encodedArgs, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling args to JSON: %w", err)
	}

	if informOpts == nil {
		informOpts = &InformOpts{}
	}

	var objectInformOpts InformOpts
	if objectWithOpts, ok := object.(ObjectWithInformArgs); ok {
		objectInformOpts = objectWithOpts.InformOpts()
	}

	// If the time is stubbed (in a test), use that for `created_at`. Otherwise,
	// leave an empty value which will either use the database's `now()` or be defaulted
	// by drivers as necessary.
	createdAt := archetype.Time.NowUTCOrNil()

	namespace := valutil.FirstNonZero(informOpts.Namespace, objectInformOpts.Namespace, deltacommon.NamespaceDefault)

	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}

	tags := informOpts.Tags
	if informOpts.Tags == nil {
		tags = objectInformOpts.Tags
	}
	if tags == nil {
		tags = []string{}
	} else {
		for _, tag := range tags {
			if len(tag) > 255 {
				return nil, errors.New("tags should be a maximum of 255 characters long")
			}
			if !tagRE.MatchString(tag) {
				return nil, errors.New("tags should match regex " + tagRE.String())
			}
		}
	}

	hash := informOpts.Hash
	if len(hash) == 0 {
		hash = objectInformOpts.Hash
	}

	metadata := informOpts.Metadata
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	insertParams := &deltatype.ObjectInformParams{
		Args:        args,
		CreatedAt:   createdAt,
		EncodedArgs: encodedArgs,
		Kind:        args.Kind(),
		MaxAttempts: maxAttempts,
		Metadata:    metadata,
		Priority:    priority,
		Queue:       queue,
		State:       rivertype.JobStateAvailable,
		Tags:        tags,
	}
	if !uniqueOpts.isEmpty() {
		internalUniqueOpts := (*dbunique.UniqueOpts)(&uniqueOpts)
		insertParams.UniqueKey, err = dbunique.UniqueKey(archetype.Time, internalUniqueOpts, insertParams)
		if err != nil {
			return nil, err
		}
		insertParams.UniqueStates = internalUniqueOpts.StateBitmask()
	}

	switch {
	case !insertOpts.ScheduledAt.IsZero():
		insertParams.ScheduledAt = &insertOpts.ScheduledAt
		insertParams.State = rivertype.JobStateScheduled
	case !jobInsertOpts.ScheduledAt.IsZero():
		insertParams.ScheduledAt = &jobInsertOpts.ScheduledAt
		insertParams.State = rivertype.JobStateScheduled
	default:
		// Use a stubbed time if there was one, but otherwise prefer the value
		// generated by the database. createdAt is nil unless time is stubbed.
		insertParams.ScheduledAt = createdAt
	}

	if insertOpts.Pending {
		insertParams.State = rivertype.JobStatePending
	}

	return insertParams, nil
}
