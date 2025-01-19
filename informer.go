package delta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
	"github.com/chariot-giving/delta/internal/object"
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
	// ResourceKind is the kind of resource to inform
	// Required parameter
	ResourceKind string `river:"unique"`
	// ProcessExisting is used to determine if the informer should process existing resources
	// The informer checks existence based on object.Compare() or hash comparison
	// Defaults to false (skip existing)
	ProcessExisting bool `river:"unique"`
	// RunForeground is used to determine if the informer should run the work in the foreground or background
	// Defaults to false (background)
	// TODO: implement this
	RunForeground bool `river:"unique"`
	// Options are optional settings for filtering resources during inform.
	Options *InformOptions
	// Object is the resource to inform
	object T
}

func (i InformArgs[T]) Kind() string {
	return "delta.inform." + i.ResourceKind
}

func (i InformArgs[T]) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 10,
		Queue:       "controller",
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	}
}

// controllerInformer is a worker that informs the controller of resources from external sources.
type controllerInformer[T Object] struct {
	factory  object.ObjectFactory
	informer Informer[T]
	river.WorkerDefaults[InformArgs[T]]
}

func (i *controllerInformer[T]) Work(ctx context.Context, job *river.Job[InformArgs[T]]) error {
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		return err
	}
	queries := sqlc.New(client.dbPool)

	controller, err := queries.ControllerGet(ctx, job.Args.ResourceKind)
	if err != nil {
		return fmt.Errorf("failed to get controller inform record: %w", err)
	}

	metadata := make(map[string]string)
	if len(job.Args.Options.Labels) > 0 {
		metadata = job.Args.Options.Labels
	}
	informOpts := InformOptions{
		Labels:  metadata,
		Since:   firstNonZero(job.Args.Options.Since, &controller.LastInformTime), // or we could take whichever is earlier?
		OrderBy: firstNonZero(job.Args.Options.OrderBy),
		Limit:   firstNonZero(job.Args.Options.Limit),
	}

	// Set a 5-minute deadline for the entire operation
	ctx, cancel := context.WithTimeout(ctx, i.Timeout(job))
	defer cancel()

	queue, err := i.informer.Inform(ctx, &informOpts)
	if err != nil {
		return fmt.Errorf("failed to inform controller: %w", err)
	}

	for {
		select {
		case obj, ok := <-queue:
			if !ok {
				// happy path: successfully informed all resources
				if !job.Args.ProcessExisting {
					// only update the controller last inform time if we aren't processing existing resources
					err := queries.ControllerSetLastInformTime(ctx, controller.Name)
					if err != nil {
						return err
					}
				}
				return nil
			}
			if err := i.processObject(ctx, obj, &job.Args); err != nil {
				return err
			}
		case <-ctx.Done():
			return fmt.Errorf("work deadline exceeded: %w", ctx.Err())
		}
	}
}

func (i *controllerInformer[T]) Timeout(job *river.Job[InformArgs[T]]) time.Duration {
	if job.Args.ProcessExisting {
		return 5 * time.Minute
	}
	return 1 * time.Minute
}

func (i *controllerInformer[T]) processObject(ctx context.Context, object T, args *InformArgs[T]) error {
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		return err
	}
	logger := middleware.LoggerFromContext(ctx)
	riverClient, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}

	tx, err := client.dbPool.Begin(ctx)
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
		if errors.Is(err, pgx.ErrNoRows) {
			objectInformOpts := InformOpts{}
			if objectWithOpts, ok := Object(object).(ObjectWithInformOpts); ok {
				objectInformOpts = objectWithOpts.InformOpts()
			}

			namespace := firstNonZero(objectInformOpts.Namespace, namespaceDefault)

			tags := objectInformOpts.Tags
			if tags == nil {
				tags = []string{}
			} else {
				for _, tag := range tags {
					if len(tag) > 255 {
						return errors.New("tags should be a maximum of 255 characters long")
					}
					if !tagRE.MatchString(tag) {
						return errors.New("tags should match regex " + tagRE.String())
					}
				}
			}

			// TODO: clean this up
			if len(objectInformOpts.Metadata) == 0 {
				objectInformOpts.Metadata = []byte(`{}`)
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
				Tags:        tags,
				Hash:        hash[:],
				MaxAttempts: 10, // TODO: make this configurable
			})
			if err != nil {
				return err
			}

			resourceRow := toResourceRow(&res.DeltaResource)
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
		return err
	}

	// comparison
	compare, err := i.compareObjects(object, toResourceRow(resource))
	if err != nil {
		return err
	}
	if compare == 0 && resource.State == sqlc.DeltaResourceStateSynced && !args.ProcessExisting {
		// if the objects are the same, the resource is synced, and we don't want to process existing, skip
		logger.Warn("skipping already processed object", "resource_id", resource.ID, "resource_kind", resource.Kind)
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

func (i *controllerInformer[T]) compareObjects(object T, resource deltatype.ResourceRow) (int, error) {
	resourceObject := i.factory.Make(&resource)
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
