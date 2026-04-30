package delta

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/maintenance"
	"github.com/chariot-giving/delta/internal/middleware"
)

// Config is the configuration for a Client.
type Config struct {
	// Logger is the structured logger to use for logging purposes. If none is
	// specified, logs will be emitted to STDOUT with messages at warn level
	// or higher.
	Logger *slog.Logger

	// Metrics receives observability signals (counters and histograms) from
	// Delta. If nil, a no-op collector is used. Implementations should
	// translate calls to whatever observability backend the application
	// uses (Prometheus, OpenTelemetry, statsd, etc.). See metrics.go for
	// the stable list of metric names emitted.
	Metrics MetricsCollector

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

	// ResourceWorkTimeout is the maximum amount of time a controlle worker is allowed to run before its
	// context is cancelled. A timeout of zero means ResourceWorkTimeout will be
	// used, whereas a value of -1 means the controller's context will not be cancelled
	// unless the Client is shutting down.
	//
	// Defaults to 1 minute.
	ResourceWorkTimeout time.Duration

	// ControllerInformTimeout is the maximum amount of time a controller informer is allowed to run before its
	// context is cancelled. A timeout of zero means ControllerInformTimeout will be
	// used, whereas a value of -1 means the controller's context will not be cancelled
	// unless the Client is shutting down.
	//
	// Defaults to 1 minutes.
	ControllerInformTimeout time.Duration

	// MaintenanceJobInterval is the interval at which the maintenance jobs
	// will run.
	//
	// Defaults to 1 minute.
	MaintenanceJobInterval time.Duration

	// ResourceInformerInterval is the interval at which the resource informer
	// will run. If this is 0, the default inform interval of 1 hour is used.
	//
	// If this is < 0, the resource informers will be disabled.
	//
	// Defaults to 1 hour.
	ResourceInformInterval time.Duration

	// ResourceCleanerTimeout is the timeout of the individual queries within the
	// resource cleaner.
	//
	// Defaults to 30 seconds, which should be more than enough time for most
	// deployments.
	ResourceCleanerTimeout time.Duration

	// DeletedResourceRetentionPeriod is the amount of time to keep deleted resources
	// around before they're removed permanently.
	//
	// Defaults to 24 hours.
	DeletedResourceRetentionPeriod time.Duration

	// SyncedResourceRetentionPeriod is the amount of time to keep synced resources
	// around before they're removed permanently.
	//
	// Defaults to 24 hours.
	SyncedResourceRetentionPeriod time.Duration

	// DegradedResourceRetentionPeriod is the amount of time to keep degraded resources
	// around before they're removed permanently.
	//
	// Defaults to 24 hours.
	DegradedResourceRetentionPeriod time.Duration

	// StuckScheduledThreshold is the grace period after which a resource still in
	// the 'scheduled' state is considered orphaned (its River scheduler job is gone)
	// and will be rescued by the rescheduler. Should be comfortably longer than the
	// normal scheduler latency to avoid racing with in-flight scheduling.
	//
	// Defaults to an hour.
	StuckScheduledThreshold time.Duration
}

// Client is a single isolated instance of Delta. Your application may use
// multiple instances operating on different databases or Postgres schemas
// within a single database.
type Client struct {
	config              *Config
	dbPool              *pgxpool.Pool
	workers             *river.Workers
	client              *river.Client[pgx.Tx]
	eventCh             chan []Event
	subscriptionManager *subscriptionManager
	metrics             MetricsCollector
}

var (
	errMissingConfig = errors.New("missing config")
)

func NewClient(dbPool *pgxpool.Pool, config *Config) (*Client, error) {
	if config == nil {
		return nil, errMissingConfig
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		}))
	}
	config.Logger = logger

	metrics := config.Metrics
	if metrics == nil {
		metrics = noopMetrics{}
	}
	config.Metrics = metrics

	config.ResourceWorkTimeout = firstNonZero(config.ResourceWorkTimeout, time.Minute*1)
	config.ControllerInformTimeout = firstNonZero(config.ControllerInformTimeout, time.Minute*1)
	config.StuckScheduledThreshold = firstNonZero(config.StuckScheduledThreshold, time.Hour*1)

	client := &Client{
		config:  config,
		dbPool:  dbPool,
		workers: river.NewWorkers(),
		metrics: metrics,
	}

	if config.willManageResources() {
		queues := map[string]river.QueueConfig{
			"controller": {
				MaxWorkers: 3,
			},
			"maintenance": {
				MaxWorkers: 1,
			},
		}

		client.subscriptionManager = &subscriptionManager{
			logger:           config.Logger.WithGroup("subscription_manager"),
			metrics:          metrics,
			subscriptions:    make(map[int]*eventSubscription),
			subscriptionsSeq: 0,
			mu:               sync.Mutex{},
		}

		// Add controller workers and underlying job queues
		for _, controller := range config.Controllers.controllerMap {
			if err := controller.configurer.Configure(client.workers); err != nil {
				return nil, fmt.Errorf("error configuring %s controller: %w", controller.object.Kind(), err)
			}

			objectSettings := ObjectSettings{}
			if objectWithSettings, ok := controller.object.(ObjectWithSettings); ok {
				objectSettings = objectWithSettings.Settings()
			}

			parallelism := firstNonZero(objectSettings.Parallelism, 1)
			if parallelism < 1 {
				return nil, errors.New("object parallelism setting must be a >= 0")
			}

			// dynamically add controller resource queues
			// this ensures the delta clients that are configured with this controller
			// will pick up the resource jobs from the underlying river queue
			// see: https://github.com/riverqueue/river/discussions/725
			queues[controller.object.Kind()] = river.QueueConfig{
				MaxWorkers: parallelism,
			}
		}

		if err := river.AddWorkerSafely(client.workers, &controllerInformerScheduler{pool: client.dbPool}); err != nil {
			return nil, fmt.Errorf("error adding controller informer scheduler worker: %w", err)
		}
		if err := river.AddWorkerSafely(client.workers, &controllerReconcileScheduler{pool: client.dbPool}); err != nil {
			return nil, fmt.Errorf("error adding controller reconcile scheduler worker: %w", err)
		}
		if err := river.AddWorkerSafely(client.workers, &rescheduler{
			pool:                    client.dbPool,
			stuckScheduledThreshold: config.StuckScheduledThreshold,
			metrics:                 metrics,
		}); err != nil {
			return nil, fmt.Errorf("error adding rescheduler worker: %w", err)
		}

		// Add maintenance workers
		// 1. expirer (expire resources)
		if err := river.AddWorkerSafely(client.workers, maintenance.NewNamespaceExpirer(client.dbPool, metrics)); err != nil {
			return nil, fmt.Errorf("error adding namespace expirer worker: %w", err)
		}
		// 2. cleaner (delete old resources that are degraded)
		if err := river.AddWorkerSafely(client.workers, maintenance.NewCleaner(client.dbPool, metrics)); err != nil {
			return nil, fmt.Errorf("error adding cleaner worker: %w", err)
		}

		// initialize river client
		riverConfig := &river.Config{
			Queues:              queues,
			Workers:             client.workers,
			SkipUnknownJobCheck: true,
			// Logger:  c.config.Logger.With("name", "riverqueue"),
			WorkerMiddleware: []rivertype.WorkerMiddleware{
				middleware.NewLoggingMiddleware(client.config.Logger),
				&jobContextMiddleware{client: client},
			},
			PeriodicJobs: []*river.PeriodicJob{
				river.NewPeriodicJob(
					river.PeriodicInterval(firstNonZero(client.config.MaintenanceJobInterval, time.Minute*1)),
					func() (river.JobArgs, *river.InsertOpts) {
						return InformScheduleArgs{}, nil
					},
					&river.PeriodicJobOpts{
						RunOnStart: true,
					},
				),
				river.NewPeriodicJob(
					river.PeriodicInterval(firstNonZero(client.config.MaintenanceJobInterval, time.Minute*1)),
					func() (river.JobArgs, *river.InsertOpts) {
						return ReconcileScheduleArgs{}, nil
					},
					&river.PeriodicJobOpts{
						RunOnStart: true,
					},
				),
				river.NewPeriodicJob(
					river.PeriodicInterval(firstNonZero(client.config.MaintenanceJobInterval, time.Minute*1)),
					func() (river.JobArgs, *river.InsertOpts) {
						return RescheduleResourceArgs{}, nil
					},
					&river.PeriodicJobOpts{
						RunOnStart: true,
					},
				),
				river.NewPeriodicJob(
					river.PeriodicInterval(firstNonZero(client.config.MaintenanceJobInterval, time.Minute*1)),
					func() (river.JobArgs, *river.InsertOpts) {
						return maintenance.ExpireResourceArgs{}, nil
					},
					&river.PeriodicJobOpts{
						RunOnStart: true,
					},
				),
				river.NewPeriodicJob(
					river.PeriodicInterval(firstNonZero(client.config.MaintenanceJobInterval, time.Minute*1)),
					func() (river.JobArgs, *river.InsertOpts) {
						return maintenance.CleanResourceArgs{
							DeletedResourceRetentionPeriod:  firstNonZero(client.config.DeletedResourceRetentionPeriod, time.Hour*24),
							SyncedResourceRetentionPeriod:   firstNonZero(client.config.SyncedResourceRetentionPeriod, time.Hour*24),
							DegradedResourceRetentionPeriod: firstNonZero(client.config.DegradedResourceRetentionPeriod, time.Hour*24),
							Timeout:                         firstNonZero(client.config.ResourceCleanerTimeout, time.Second*30),
						}, nil
					},
					&river.PeriodicJobOpts{
						RunOnStart: true,
					},
				),
			},
		}

		riverClient, err := river.NewClient(riverpgxv5.New(client.dbPool), riverConfig)
		if err != nil {
			return nil, fmt.Errorf("error creating river client: %w", err)
		}
		client.client = riverClient
	}

	return client, nil
}

func (c *Client) Start(ctx context.Context) error {
	if !c.config.willManageResources() {
		return errors.New("client Namespaces and Controllers must be configured for a client to start managing resources")
	}
	if c.config.Controllers != nil && len(c.config.Controllers.controllerMap) < 1 {
		return errors.New("at least one Controller must be added to the Controllers bundle")
	}

	queries := sqlc.New(c.dbPool)

	// seed the initial namespaces
	for namespace, config := range c.config.Namespaces {
		if err := validateNamespace(namespace); err != nil {
			return err
		}

		if config.SyncedResourceRetentionPeriod > 0 && config.ResourceExpiry > 0 {
			return fmt.Errorf("namespace %q: cannot set both SyncedResourceRetentionPeriod and ResourceExpiry", namespace)
		}

		// TODO: override synced, degraded, and deleted resource retention periods
		// how do you ensure multiple clients don't override namespaces & associated cleaner settings?

		_, err := queries.NamespaceCreateOrSetUpdatedAt(ctx, &sqlc.NamespaceCreateOrSetUpdatedAtParams{
			Name:      namespace,
			ExpiryTtl: int32(min(float64(config.ResourceExpiry.Milliseconds()/1000), float64(math.MaxInt32))),
		})
		if err != nil {
			return err
		}
	}

	// seed the initial controllers
	for _, controller := range c.config.Controllers.controllerMap {
		objectSettings := ObjectSettings{}
		if objectWithSettings, ok := controller.object.(ObjectWithSettings); ok {
			objectSettings = objectWithSettings.Settings()
		}

		informInterval := firstNonZero(objectSettings.InformInterval, c.config.ResourceInformInterval, time.Hour*1)

		// Validate reconciliation interval vs effective synced retention.
		// Reconciliation runs `Inform` with ProcessExisting=true, so it
		// needs synced rows to still be in the database when it runs.
		// If the cleaner or expirer would remove them first, the
		// reconciliation can never observe their state.
		if objectSettings.ReconciliationInterval > 0 {
			if err := c.config.validateReconcileRetention(controller.object.Kind(), objectSettings.ReconciliationInterval); err != nil {
				return err
			}
		}

		var reconcileIntervalArg *time.Duration
		if objectSettings.ReconciliationInterval != 0 {
			reconcileIntervalArg = &objectSettings.ReconciliationInterval
		}

		_, err := queries.ControllerCreateOrSetUpdatedAt(ctx, &sqlc.ControllerCreateOrSetUpdatedAtParams{
			Name:              controller.object.Kind(),
			Metadata:          json.RawMessage(`{}`),
			InformInterval:    &informInterval,
			ReconcileInterval: reconcileIntervalArg,
		})
		if err != nil {
			return err
		}
	}

	// configure event subscription channel
	eventCh := make(chan []Event, 10)
	c.eventCh = eventCh
	c.subscriptionManager.ResetEventChan(eventCh)
	go c.subscriptionManager.Start(ctx)

	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("error starting river client: %w", err)
	}

	return nil
}

// validateReconcileRetention enforces that reconcileInterval is shorter
// than the effective synced-resource retention for every namespace whose
// retention applies to this kind. Namespaces are not bound to a specific
// kind by config — any namespace can hold any kind — so we conservatively
// validate against every configured namespace.
func (c *Config) validateReconcileRetention(kind string, reconcileInterval time.Duration) error {
	for namespace, ns := range c.Namespaces {
		// ResourceExpiry: synced rows are flipped to expired and re-worked
		// after this duration. Reconciliation needs to win the race so it
		// can observe the synced state before the expirer perturbs it.
		if ns.ResourceExpiry > 0 && reconcileInterval >= ns.ResourceExpiry {
			return fmt.Errorf(
				"controller %q ReconciliationInterval (%s) must be shorter than namespace %q ResourceExpiry (%s); otherwise resources will be expired before reconciliation can observe them",
				kind, reconcileInterval, namespace, ns.ResourceExpiry,
			)
		}
		// SyncedResourceRetentionPeriod: synced rows are hard-deleted
		// after this duration. Reconciliation needs them to still exist.
		retention := firstNonZero(ns.SyncedResourceRetentionPeriod, c.SyncedResourceRetentionPeriod)
		if retention > 0 && ns.ResourceExpiry == 0 && reconcileInterval >= retention {
			return fmt.Errorf(
				"controller %q ReconciliationInterval (%s) must be shorter than namespace %q SyncedResourceRetentionPeriod (%s); otherwise resources will be cleaned before reconciliation can observe them",
				kind, reconcileInterval, namespace, retention,
			)
		}
	}
	return nil
}

func (c *Client) Stop(ctx context.Context) error {
	if err := c.client.Stop(ctx); err != nil {
		return fmt.Errorf("error stopping river client: %w", err)
	}

	return nil
}

// Inform the Delta system of an object.
func (c *Client) Inform(ctx context.Context, object Object, opts *InformOpts) (*deltatype.ObjectInformResult, error) {
	tx, err := c.dbPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, err := c.InformTx(ctx, tx, object, opts)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}

	return result, nil
}

// InformTx is the same as Inform but allows you to pass in a transaction.
func (c *Client) InformTx(ctx context.Context, tx pgx.Tx, object Object, opts *InformOpts) (*deltatype.ObjectInformResult, error) {
	queries := sqlc.New(tx)

	objectInformOpts := InformOpts{}
	if objectWithOpts, ok := object.(ObjectWithInformOpts); ok {
		objectInformOpts = objectWithOpts.InformOpts()
	}

	namespace := firstNonZero(opts.Namespace, objectInformOpts.Namespace, namespaceDefault)

	tags := opts.Tags
	if opts.Tags == nil {
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

	metadata := opts.Metadata
	if opts.Metadata == nil {
		metadata = objectInformOpts.Metadata
	}
	if metadata == nil {
		metadata = []byte(`{}`)
	}

	objBytes, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(objBytes)

	var externalCreatedAt *time.Time
	if objectWithCreatedAt, ok := object.(ObjectWithCreatedAt); ok {
		createdAt := objectWithCreatedAt.CreatedAt()
		if !createdAt.IsZero() {
			externalCreatedAt = &createdAt
		}
	}

	maxAttempts := firstNonZero(opts.MaxAttempts, objectInformOpts.MaxAttempts, int16(10))

	res, err := queries.ResourceCreateOrUpdate(ctx, &sqlc.ResourceCreateOrUpdateParams{
		ObjectID:          object.ID(),
		Kind:              object.Kind(),
		Namespace:         namespace,
		State:             sqlc.DeltaResourceStateScheduled,
		Object:            objBytes,
		Metadata:          metadata,
		Tags:              tags,
		Hash:              hash[:],
		ExternalCreatedAt: externalCreatedAt,
		MaxAttempts:       maxAttempts,
	})
	if err != nil {
		return nil, err
	}

	// We need to Insert the generic Resource struct into the river queue.
	// In order to do this, we leverage the controller's objectFactory.
	// This isn't ideal because it requires that the process invoking Inform
	// has instantiated the associated controllers for the resource object kind.
	// This means you can't have controller-less clients that can inform resources.
	controller, ok := c.config.Controllers.controllerMap[object.Kind()]
	if !ok {
		return nil, fmt.Errorf("controller for kind %q is not registered", object.Kind())
	}

	resourceRow := toResourceRow(&res.DeltaResource)
	objectWrapper := controller.objectFactory.Make(&resourceRow)
	if err := objectWrapper.UnmarshalResource(); err != nil {
		return nil, err
	}

	if err := objectWrapper.Enqueue(ctx, tx, c.client); err != nil {
		return nil, err
	}

	return &deltatype.ObjectInformResult{
		Resource:      &resourceRow,
		AlreadyExists: !res.IsInsert,
	}, nil
}

// ScheduleInform schedules an inform job for a controller to sync resources.
func (c *Client) ScheduleInform(ctx context.Context, params ScheduleInformParams, informOpts *InformOptions) error {
	_, err := c.client.Insert(ctx, InformArgs[kindObject]{
		ResourceKind:    params.ResourceKind,
		ProcessExisting: params.ProcessExisting,
		RunForeground:   params.RunForeground,
		Options:         informOpts,
		object:          kindObject{kind: params.ResourceKind},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to insert inform job: %w", err)
	}

	return nil
}

type ScheduleInformParams struct {
	ResourceKind    string
	ProcessExisting bool
	RunForeground   bool
}

// Invalidate marks a resource as expired.
// This will cause the resource to be re-enqueued for processing/syncing.
// Normally, this is done automatically by the expirer maintenance job
// if the resource exists within a namespace that has an expiry ttl.
//
// If the resource does not exist within a namespace that has an expiry ttl,
// then you must manually call Invalidate to re-enqueue the resource.
//
// This is useful if you want to re-enqueue a resource that was previously
// synced, but should be re-processed for some reason.
func (c *Client) Invalidate(ctx context.Context, object Object) (*deltatype.ResourceRow, error) {
	queries := sqlc.New(c.dbPool)

	resource, err := queries.ResourceGetByObjectIDAndKind(ctx, &sqlc.ResourceGetByObjectIDAndKindParams{
		ObjectID: object.ID(),
		Kind:     object.Kind(),
	})
	if err != nil {
		return nil, err
	}

	if resource.State != sqlc.DeltaResourceStateSynced {
		return nil, fmt.Errorf("resource is %s, cannot invalidate; only synced resources can be invalidated", resource.State)
	}

	updated, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:      resource.ID,
		Column1: true,
		State:   sqlc.DeltaResourceStateExpired,
	})
	if err != nil {
		return nil, err
	}

	resourceRow := toResourceRow(updated)
	return &resourceRow, nil
}

// Subscribe subscribes to the provided categories of object events that occur
// within the client, like ObjectCreated for when an object is created.
//
// Returns a channel over which to receive events along with a cancel function
// that can be used to cancel and tear down resources associated with the
// subscription. It's recommended but not necessary to invoke the cancel
// function. Resources will be freed when the client stops in case it's not.
//
// The event channel is buffered and sends on it are non-blocking. Consumers
// must process events in a timely manner or it's possible for events to be
// dropped. Any slow operations performed in a response to a receipt (e.g.
// persisting to a database) should be made asynchronous to avoid event loss.
//
// Callers must specify the categories of events they're interested in. This allows
// for forward compatibility in case new categories of events are added in future
// versions. If new event categories are added, callers will have to explicitly add
// them to their requested list and ensure they can be handled correctly.
func (c *Client) Subscribe(cateogories ...EventCategory) (<-chan Event, func()) {
	return c.subscribeConfig(&SubscribeConfig{Categories: cateogories})
}

// Special internal variant that lets us inject an overridden size.
func (c *Client) subscribeConfig(config *SubscribeConfig) (<-chan Event, func()) {
	if c.subscriptionManager == nil {
		panic("created a subscription on a client that will never work resources (Controllers not configured)")
	}

	return c.subscriptionManager.SubscribeConfig(config)
}

// emitEvent ships an event onto the client's distribution channel without
// blocking the caller. If the client hasn't been started yet (eventCh nil)
// or the buffered channel is full, the event is dropped and a metric
// increment is recorded so the loss is observable.
func (c *Client) emitEvent(ctx context.Context, event Event) {
	if c == nil || c.eventCh == nil {
		return
	}
	select {
	case c.eventCh <- []Event{event}:
	default:
		c.metrics.Counter(ctx, MetricSubscriptionDropped, 1, map[string]string{
			"category": string(event.EventCategory),
		})
		c.config.Logger.WarnContext(ctx, "delta: dropped event before fanout due to full bus", "category", event.EventCategory)
	}
}

// Indicates whether with the given configuration, this client will be expected
// to manage resources (rather than just being used to inform them). Managing resources
// requires a set of configured namespaces.
func (c *Config) willManageResources() bool {
	return len(c.Namespaces) > 0
}
