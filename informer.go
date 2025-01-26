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
	//
	// // It is important to respect context cancellation to enable
	// the delta client to respond to shutdown requests.
	// There is no way to cancel a running controller that does not respect
	// context cancellation, other than terminating the process.
	Inform(ctx context.Context, opts *InformOptions) (<-chan T, error)
	// InformTimeout is the maximum amount of time the inform job is allowed to run before
	// its context is cancelled. A timeout of zero (the default) means the job
	// will inherit the Client-level timeout (defaults to 1 minute).
	// A timeout of -1 means the job's context will never time out.
	InformTimeout(args *InformArgs[T]) time.Duration
}

// InformerDefaults is an empty struct that can be embedded in your controller
// struct to make it fulfill the Informer interface with default values.
type InformerDefaults[T Object] struct{}

// Timeout returns the inform arg-specific timeout. Override this method to set a
// inform-specific timeout, otherwise the Client-level timeout will be applied.
func (w InformerDefaults[T]) InformTimeout(*InformArgs[T]) time.Duration { return 0 }

type InformArgs[T Object] struct {
	// ResourceKind is the kind of resource to inform
	// Required parameter
	ResourceKind string
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
	return "delta.inform." + i.object.Kind()
}

func (i InformArgs[T]) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 10,
		Queue:       i.Kind(),
		UniqueOpts: river.UniqueOpts{
			ByQueue: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRetryable,
				rivertype.JobStateRunning,
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

	informFunc := func(ctx context.Context) error {
		timeout := i.informer.InformTimeout(&job.Args)
		if timeout == 0 {
			// use the client-level timeout if the resource doesn't specify one
			timeout = client.config.ControllerInformTimeout
		}

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

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

	return informFunc(ctx)
}

func (i *controllerInformer[T]) Timeout(job *river.Job[InformArgs[T]]) time.Duration {
	// we enforce our own timeout so we want to remove River's underlying timeout on the job
	return -1
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
				return fmt.Errorf("failed to create or update resource: %w", err)
			}

			resourceRow := toResourceRow(&res.DeltaResource)
			_, err = riverClient.InsertTx(ctx, tx, Resource[T]{Object: object, ResourceRow: &resourceRow}, &river.InsertOpts{
				Queue:    object.Kind(),
				Tags:     resourceRow.Tags,
				Metadata: resourceRow.Metadata,
			})
			if err != nil {
				return err
			}

			return tx.Commit(ctx)
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// handle soft-deleted resources
	if resource.State == sqlc.DeltaResourceStateDeleted {
		logger.WarnContext(ctx, "skipping soft-deleted resource", "id", resource.ID, "kind", resource.Kind, "object_id", resource.ObjectID)
		return nil
	}

	// comparison
	compare, err := i.compareObjects(object, toResourceRow(resource))
	if err != nil {
		return err
	}
	if compare == 0 && resource.State == sqlc.DeltaResourceStateSynced && !args.ProcessExisting {
		// if the objects are the same, the resource is synced, and we don't want to process existing, skip
		logger.WarnContext(ctx, "skipping already processed object", "id", resource.ID, "kind", resource.Kind, "object_id", resource.ObjectID)
		return nil
	}

	// update the resource to be scheduled
	// TODO: likely want to update object and hash state here
	// but may not want to pre-emptively do it before the worker...?
	updated, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:       resource.ID,
		Column1:  true,
		State:    sqlc.DeltaResourceStateScheduled,
		Column3:  true,
		SyncedAt: nil,
	})
	if err != nil {
		return fmt.Errorf("failed to set resource state: %w", err)
	}

	resourceRow := toResourceRow(updated)
	_, err = riverClient.InsertTx(ctx, tx, Resource[T]{Object: object, ResourceRow: &resourceRow}, &river.InsertOpts{
		Queue:    object.Kind(),
		Tags:     resourceRow.Tags,
		Metadata: resourceRow.Metadata,
	})
	if err != nil {
		return fmt.Errorf("failed to enqueue resource: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
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
