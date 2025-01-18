package delta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/object"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// InformOptions are optional settings for filtering resources during inform.
// These are kept purposefully generic and it's up to the application to define
// what they mean.
type InformOptions struct {
	// Labels is a map of key-value pairs that can be used to filter resources
	Labels map[string]string
	// Since allows filtering resources modified after a specific time
	// TODO: ideally, there is some state kept in delta DB that tracks controller inform jobs
	// and saves down the last successful inform time so subsequent informs can be filtered
	// to only inform resources that have changed since the last successful inform time.
	Since *time.Time
	// Limit sets the maximum number of resources to return (0 means no limit)
	Limit int
	// OrderBy specifies the field and direction for sorting (e.g., "created_at DESC")
	OrderBy string
}

// Informer is an interface that provides a way to inform the controller of resources.
type Informer[T Object] interface {
	// Inform informs the controller of resources.
	Inform(ctx context.Context, opts *InformOptions) (<-chan T, error)
}

type InformArgs[T Object] struct {
	// The ID of the controller inform record
	ID int32
	// ResourceKind is the kind of resource to inform
	// Required parameter
	ResourceKind string `river:"unique"`
	// ProcessExisting is used to determine if the informer should process existing resources
	// The informer checks existence based on object.Compare() or hash comparison
	// Defaults to false (skip existing)
	ProcessExisting bool
	// RunForeground is used to determine if the informer should run the work in the foreground or background
	// Defaults to false (background)
	// TODO: implement this
	RunForeground bool
	// Options are optional settings for filtering resources during inform.
	Options *InformOptions
	// Object is the resource to inform
	object T
}

func (i InformArgs[T]) Kind() string {
	return fmt.Sprintf("delta.inform.%s", i.object.Kind())
}

func (i InformArgs[T]) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 10,
		Queue:       "controller",
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: time.Hour * 1,
		},
	}
}

// controllerInformer is a worker that informs the controller of resources from external sources.
// It is meant to be run as a periodic job, but can also be run adhoc.
type controllerInformer[T Object] struct {
	pool       *pgxpool.Pool
	factory    object.ObjectFactory
	controller Controller[T]
	river.WorkerDefaults[InformArgs[T]]
}

func (w *controllerInformer[T]) Work(ctx context.Context, job *river.Job[InformArgs[T]]) error {
	queries := sqlc.New(w.pool)

	inform, err := queries.ControllerInformGet(ctx, job.Args.ID)
	if err != nil {
		return fmt.Errorf("failed to get controller inform record: %w", err)
	}

	var opts InformOptions
	if inform.Opts != nil {
		if err := json.Unmarshal(inform.Opts, &opts); err != nil {
			return fmt.Errorf("failed to unmarshal inform opts: %w", err)
		}
	}

	metadata := opts.Labels
	if len(job.Args.Options.Labels) > 0 {
		metadata = job.Args.Options.Labels
	}
	informOpts := InformOptions{
		Labels:  metadata,
		Since:   firstNonZero(job.Args.Options.Since, inform.LastInformTime),
		OrderBy: firstNonZero(job.Args.Options.OrderBy, opts.OrderBy),
		Limit:   firstNonZero(job.Args.Options.Limit, opts.Limit),
	}

	// Set a 5-minute deadline for the entire operation
	ctx, cancel := context.WithTimeout(ctx, w.Timeout(job))
	defer cancel()

	var numResources int64

	log.Println("Starting Inform")
	queue, err := w.controller.Inform(ctx, &informOpts)
	if err != nil {
		return fmt.Errorf("failed to inform controller: %w", err)
	}

	for {
		select {
		case obj, ok := <-queue:
			if !ok {
				log.Println("Inform finished")
				// happy path: successfully informed all resources
				// this is to keep track of the last successful inform time
				// so that we can filter resources that have changed since then
				// in subsequent inform jobs
				err := queries.ControllerInformSetInformed(ctx, &sqlc.ControllerInformSetInformedParams{
					ID:           job.Args.ID,
					NumResources: numResources,
				})
				if err != nil {
					return err
				}
				return nil
			}
			numResources++
			log.Printf("Processing object: %v", obj)
			if err := w.processObject(ctx, obj, &job.Args); err != nil {
				return err
			}
		case <-ctx.Done():
			return fmt.Errorf("work deadline exceeded: %w", ctx.Err())
		}
	}
}

func (w *controllerInformer[T]) Timeout(job *river.Job[InformArgs[T]]) time.Duration {
	if job.Args.ProcessExisting {
		return 5 * time.Minute
	}
	return 1 * time.Minute
}

func (w *controllerInformer[T]) processObject(ctx context.Context, object T, args *InformArgs[T]) error {
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	queries := sqlc.New(tx)

	resource, err := queries.ResourceGetByObjectIDAndKind(ctx, &sqlc.ResourceGetByObjectIDAndKindParams{
		ObjectID: object.ID(),
		Kind:     object.Kind(),
	})
	if err != nil {
		if pgx.ErrNoRows == err {
			objectInformOpts := InformOpts{}
			if objectWithOpts, ok := Object(object).(ObjectWithInformOpts); ok {
				objectInformOpts = objectWithOpts.InformOpts()
			}

			namespace := firstNonZero(objectInformOpts.Namespace, namespaceDefault)

			// TODO: clean this up
			if len(objectInformOpts.Metadata) == 0 {
				objectInformOpts.Metadata = []byte(`{}`)
			}
			if len(objectInformOpts.Tags) == 0 {
				objectInformOpts.Tags = []string{}
			}

			objBytes, err := json.Marshal(object)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(objBytes)

			res, err := queries.ResourceCreateOrUpdate(ctx, &sqlc.ResourceCreateOrUpdateParams{
				ObjectID:    object.ID(),
				Kind:        object.Kind(),
				Namespace:   namespace,
				State:       sqlc.DeltaResourceStateScheduled,
				Object:      objBytes,
				Metadata:    objectInformOpts.Metadata,
				Tags:        objectInformOpts.Tags,
				Hash:        hash[:],
				MaxAttempts: 10, // TODO: make this configurable
			})
			if err != nil {
				return err
			}

			if res.IsInsert {
				resourceRow := toResourceRow(&res.DeltaResource)
				_, err = riverClient.InsertTx(ctx, tx, Resource[T]{Object: object, ResourceRow: &resourceRow}, &river.InsertOpts{
					Queue:    "resource",
					Tags:     resourceRow.Tags,
					Metadata: resourceRow.Metadata,
				})
				if err != nil {
					return err
				}
			} else {
				// TODO: how do we want to handle updates?
			}

			return tx.Commit(ctx)
		}
		return err
	}

	// comparison
	compare, err := w.compareObjects(object, toResourceRow(resource))
	if err != nil {
		return err
	}
	if compare == 0 && !args.ProcessExisting {
		// if the objects are the same and we don't want to process existing, skip
		return nil
	}

	// update the resource to be scheduled
	// TODO: likely want to update object and hash state here
	// but may not want to pre-emptively do it before the worker...?
	updated, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:      resource.ID,
		Column1: true,
		State:   sqlc.DeltaResourceStateScheduled,
	})
	if err != nil {
		return err
	}

	resourceRow := toResourceRow(updated)
	_, err = riverClient.InsertTx(ctx, tx, Resource[T]{Object: object, ResourceRow: &resourceRow}, &river.InsertOpts{
		Queue:    "resource",
		Tags:     resourceRow.Tags,
		Metadata: resourceRow.Metadata,
	})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (w *controllerInformer[T]) compareObjects(object T, resource deltatype.ResourceRow) (int, error) {
	resourceObject := w.factory.Make(&resource)
	if err := resourceObject.UnmarshalResource(); err != nil {
		return 0, fmt.Errorf("failed to unmarshal resource: %w", err)
	}

	if compare, ok := resourceObject.Compare(object); ok {
		return compare, nil
	}

	// check hash
	// we lose ordering with the hash (that's why we prefer the object comparison)
	objBytes, err := json.Marshal(object)
	if err != nil {
		return 0, err
	}
	hash := sha256.Sum256(objBytes)
	if bytes.Equal(resource.Hash, hash[:]) {
		return 0, nil
	}
	return 1, nil
}
