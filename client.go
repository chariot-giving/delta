package delta

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/chariot-giving/delta/deltacommon"
	"github.com/chariot-giving/delta/deltashared/util/valutil"
	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
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

	// ObjectInformMiddleware are optional functions that can be called around object
	// inform.
	ObjectInformMiddleware []deltatype.ObjectInformMiddleware

	// Namespaces is a list of namespaces for this client to operate on along
	// with configuration for each namespace.
	//
	// This field may be omitted for a program that's only informing resources rather
	// than managing them. If it's specified, then Controllers must also be given.
	Namespaces map[string]NamespaceConfig
}

// Client is a single isolated instance of Delta. Your application may use
// multiple instances operating on different databases or Postgres schemas
// within a single database.
type Client struct {
	config  *Config
	dbPool  *pgxpool.Pool
	workers *river.Workers
	client  *river.Client[pgx.Tx]
}

func NewClient(dbPool *pgxpool.Pool, config Config) (*Client, error) {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		}))
	}
	config.Logger = logger

	c := &Client{
		config:  &config,
		dbPool:  dbPool,
		workers: river.NewWorkers(),
	}

	// Add controller workers to river workers
	for _, controller := range config.Controllers.controllerMap {
		river.AddWorker(c.workers, controller.worker)
	}

	// Add controller informers to river workers
	for _, controller := range config.Controllers.controllerMap {
		river.AddWorker(c.workers, controller.informer)
	}

	// add generic informer delegator/scheduler
	river.AddWorker(c.workers, &controllerInformerScheduler{pool: c.dbPool})

	// Add maintenance workers to river workers
	// 1. expirer (expire resources): easy to make stateless as it's maintenance
	// 2. cleaner (delete old resources that are degraded): easy to make stateless as it's maintenance
	// 3. reenqueuer (re-enqueue expired resources/objects to be worked): easy to make stateless as it's maintenance

	// If someone wanted to manually mark a bunch of objects to be re-worked
	// They could just manually expire the objects which will then get automatically re-enqueued.

	// initialize river client
	riverConfig := &river.Config{
		Queues: map[string]river.QueueConfig{
			"controller": {
				MaxWorkers: 3,
			},
			"resource": {
				MaxWorkers: 5,
			},
			"maintenance": {
				MaxWorkers: 1,
			},
		},
		Workers: c.workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour*2),
				func() (river.JobArgs, *river.InsertOpts) {
					return InformScheduleArgs{
						InformInterval: time.Hour * 1,
					}, nil
				},
				&river.PeriodicJobOpts{
					RunOnStart: true,
				},
			),
		},
	}
	client, err := river.NewClient(riverpgxv5.New(c.dbPool), riverConfig)
	if err != nil {
		return nil, err
	}
	c.client = client

	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
	for namespace, config := range c.config.Namespaces {
		if err := validateNamespace(namespace); err != nil {
			return err
		}

		_, err := sqlc.New(c.dbPool).NamespaceCreateOrSetUpdatedAt(ctx, &sqlc.NamespaceCreateOrSetUpdatedAtParams{
			Name:           namespace,
			ResourceExpiry: int32(config.ResourceExpiry),
		})
		if err != nil {
			return err
		}
	}
	return c.client.Start(ctx)
}

func (c *Client) Stop(ctx context.Context) error {
	return c.client.Stop(ctx)
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
	if objectWithOpts, ok := Object(object).(ObjectWithInformOpts); ok {
		objectInformOpts = objectWithOpts.InformOpts()
	}

	namespace := valutil.FirstNonZero(opts.Namespace, objectInformOpts.Namespace, deltacommon.NamespaceDefault)

	objBytes, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(objBytes)

	res, err := queries.ResourceCreateOrUpdate(ctx, &sqlc.ResourceCreateOrUpdateParams{
		ObjectID:  object.ID(),
		Kind:      object.Kind(),
		Namespace: namespace,
		State:     sqlc.DeltaResourceStateScheduled,
		Object:    objBytes,
		Metadata:  objectInformOpts.Metadata,
		Tags:      objectInformOpts.Tags,
		Hash:      hash[:],
	})
	if err != nil {
		return nil, err
	}

	_, err = c.client.InsertTx(ctx, tx, object, &river.InsertOpts{
		Queue:    "resource",
		Tags:     objectInformOpts.Tags,
		Metadata: objectInformOpts.Metadata,
	})
	if err != nil {
		return nil, err
	}

	return &deltatype.ObjectInformResult{
		Resource: &deltatype.ResourceRow{
			ID:            res.ID,
			ObjectID:      res.ObjectID,
			Kind:          res.Kind,
			Namespace:     res.Namespace,
			EncodedObject: res.Object,
			Hash:          res.Hash,
			Metadata:      res.Metadata,
			CreatedAt:     res.CreatedAt,
			SyncedAt:      res.SyncedAt,
			Attempt:       int(res.Attempt),
			MaxAttempts:   int(res.MaxAttempts),
			State:         deltatype.ResourceState(res.State),
			Tags:          res.Tags,
		},
		AlreadyExists: !res.IsInsert,
	}, nil
}
