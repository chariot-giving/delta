package delta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/chariot-giving/delta/deltacommon"
	"github.com/chariot-giving/delta/deltashared/util/valutil"
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
	Inform(ctx context.Context, queue chan T, opts *InformOptions) error
}

type InformArgs struct {
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
}

func (i InformArgs) Kind() string {
	return fmt.Sprintf("delta.controller.%s.inform", i.ResourceKind)
}

func (i InformArgs) InsertOpts() river.InsertOpts {
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
type controllerInformer struct {
	pool       *pgxpool.Pool
	factory    object.ObjectFactory
	controller controllerInterface
	river.WorkerDefaults[InformArgs]
}

func (w *controllerInformer) Work(ctx context.Context, job *river.Job[InformArgs]) error {
	// Set a 5-minute deadline for the entire operation
	ctx, cancel := context.WithTimeout(ctx, w.Timeout(job))
	defer cancel()

	queue := make(chan Object)

	// Create error channel to catch errors from the Inform goroutine
	errChan := make(chan error, 1)

	go func() {
		defer close(queue)
		errChan <- w.controller.Inform(ctx, queue, job.Args.Options)
	}()

	for {
		select {
		case obj, ok := <-queue:
			if !ok {
				// Channel closed, check for errors from Inform
				if err := <-errChan; err != nil {
					return fmt.Errorf("inform error: %w", err)
				}
				return nil
			}
			if err := w.processObject(ctx, obj, &job.Args); err != nil {
				return err
			}
		case <-ctx.Done():
			return fmt.Errorf("work deadline exceeded: %w", ctx.Err())
		}
	}
}

func (w *controllerInformer) Timeout(job *river.Job[InformArgs]) time.Duration {
	if job.Args.ProcessExisting {
		return 5 * time.Minute
	}
	return 1 * time.Minute
}

func (w *controllerInformer) processObject(ctx context.Context, obj Object, args *InformArgs) error {
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
		ObjectID: obj.ID(),
		Kind:     obj.Kind(),
	})
	if err != nil {
		if pgx.ErrNoRows == err {
			objectInformOpts := InformOpts{}
			if objectWithOpts, ok := Object(obj).(ObjectWithInformOpts); ok {
				objectInformOpts = objectWithOpts.InformOpts()
			}

			namespace := valutil.FirstNonZero(objectInformOpts.Namespace, deltacommon.NamespaceDefault)

			objBytes, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(objBytes)

			_, err = queries.ResourceCreateOrUpdate(ctx, &sqlc.ResourceCreateOrUpdateParams{
				ObjectID:  obj.ID(),
				Kind:      obj.Kind(),
				Namespace: namespace,
				State:     sqlc.DeltaResourceStateScheduled,
				Object:    objBytes,
				Metadata:  objectInformOpts.Metadata,
				Tags:      objectInformOpts.Tags,
				Hash:      hash[:],
			})
			if err != nil {
				return err
			}

			// and then enqueue a river job
			_, err = riverClient.InsertTx(ctx, tx, obj, &river.InsertOpts{
				Queue:    "resource",
				Tags:     objectInformOpts.Tags,
				Metadata: objectInformOpts.Metadata,
			})
			if err != nil {
				return err
			}

			return tx.Commit(ctx)
		}
		return err
	}
	resourceRow := deltatype.ResourceRow{
		ID:            resource.ID,
		ObjectID:      resource.ObjectID,
		Kind:          resource.Kind,
		Namespace:     resource.Namespace,
		EncodedObject: resource.Object,
		Hash:          resource.Hash,
		Metadata:      resource.Metadata,
		CreatedAt:     resource.CreatedAt,
		SyncedAt:      resource.SyncedAt,
		Attempt:       int(resource.Attempt),
		MaxAttempts:   int(resource.MaxAttempts),
		State:         deltatype.ResourceState(resource.State),
		Tags:          resource.Tags,
	}

	// comparison
	compare, err := w.compareObjects(obj, resourceRow)
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
	_, err = queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:      resource.ID,
		Column1: true,
		State:   sqlc.DeltaResourceStateScheduled,
	})
	if err != nil {
		return err
	}

	_, err = riverClient.InsertTx(ctx, tx, obj, &river.InsertOpts{
		Queue:    "resource",
		Tags:     resourceRow.Tags,
		Metadata: resourceRow.Metadata,
	})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (w *controllerInformer) compareObjects(object Object, resource deltatype.ResourceRow) (int, error) {
	resourceObject := w.factory.Make(&resource)
	if err := resourceObject.UnmarshalResource(); err != nil {
		return 0, fmt.Errorf("failed to unmarshal resource: %w", err)
	}
	// ugly but it should work?
	if comparable, ok := any(object).(ComparableObject); ok {
		// TODO: not sure this un-generic cast works
		if wrappedObj, ok := resourceObject.(*wrapperObject[Object]); ok {
			// Add type check to ensure objects are of the same concrete type
			if reflect.TypeOf(object) == reflect.TypeOf(wrappedObj.resource.Object) {
				return comparable.Compare(wrappedObj.resource.Object), nil
			}
		}
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
