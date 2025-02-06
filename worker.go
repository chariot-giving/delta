package delta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/middleware"
	"github.com/chariot-giving/delta/internal/object"
)

// Worker is an interface that can perform work on a resource object of type T.
// The Worker interface is one of two interfaces that all Controller[T]s must implement.
type Worker[T Object] interface {
	// Work performs the work on the resource object. This method must be idempotent.
	// The context will be configured with a timeout according to the worker settings
	// and may be canceled for other reasons.
	//
	// If no error is returned, the resource will be marked as synced.
	//
	// It is important to respect context cancellation to enable
	// the delta client to respond to shutdown requests.
	// There is no way to cancel a running resource that does not respect
	// context cancellation, other than terminating the process.
	Work(ctx context.Context, resource *Resource[T]) error
	// ResourceTimeout is the maximum amount of time the resource is allowed to be worked before
	// its context is cancelled. A timeout of zero (the default) means the job
	// will inherit the Client-level timeout (defaults to 1 minute).
	// A timeout of -1 means the job's context will never time out.
	ResourceTimeout(resource *Resource[T]) time.Duration
}

// WorkerDefaults is an empty struct that can be embedded in your controller
// struct to make it fulfill the Worker interface with default values.
type WorkerDefaults[T Object] struct{}

// ResourceTimeout returns the resource-specific timeout. Override this method to set a
// resource-specific timeout, otherwise the Client-level timeout will be applied.
func (w WorkerDefaults[T]) ResourceTimeout(*Resource[T]) time.Duration { return 0 }

type controllerWorker[T Object] struct {
	factory object.ObjectFactory
	river.WorkerDefaults[Resource[T]]
}

func (w *controllerWorker[T]) Work(ctx context.Context, job *river.Job[Resource[T]]) error {
	logger := middleware.LoggerFromContext(ctx)
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		return err
	}

	resource := job.Args

	// we can't use a transaction here because it could deadlock the worker
	// since the worker function runs application code and therefore could
	// run their own nested (inner) transactions.
	//
	// We could use an advisory lock if we run into concurrency issues
	// but at least for now, we are protecting against lock contention
	// by enforcing argument uniqueness on this river job.
	// Its not perfect but it should be good enough for now.
	queries := sqlc.New(client.dbPool)

	// first check that the resource still exists in the DB
	// if it doesn't, we don't want to re-work it and instead can cancel the job
	sqlcRow, err := queries.ResourceUpdateAndGetByObjectIDAndKind(ctx, &sqlc.ResourceUpdateAndGetByObjectIDAndKindParams{
		ObjectID: job.Args.ObjectID,
		Kind:     job.Args.ObjectKind,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return river.JobCancel(fmt.Errorf("resource %s:%s no longer exists: %w", resource.Object.Kind(), resource.Object.ID(), err))
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	resourceRow := toResourceRow(sqlcRow)

	// should we use the DB resource row or the job.Args resource?
	logger.DebugContext(ctx, "working resource", "id", resourceRow.ID, "resource_id", resourceRow.ID, "resource_kind", resourceRow.Kind, "attempt", resourceRow.Attempt)

	object := w.factory.Make(&resourceRow)
	if err := object.UnmarshalResource(); err != nil {
		return fmt.Errorf("failed to unmarshal resource: %w", err)
	}

	workFunc := func(ctx context.Context) error {
		timeout := object.Timeout()
		if timeout == 0 {
			// use the client-level timeout if the resource doesn't specify one
			timeout = client.config.ResourceWorkTimeout
		}

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		if err := object.Work(ctx); err != nil {
			return err
		}
		return nil
	}

	// do the work!
	err = workFunc(ctx)
	if err != nil {
		// handle resource delete error
		deleteErr := new(ResourceDeleteError)
		if errors.Is(err, deleteErr) {
			now := time.Now().UTC()
			errorData, err := json.Marshal(deltatype.AttemptError{
				At:      now,
				Attempt: resourceRow.Attempt,
				Error:   err.Error(),
			})
			if err != nil {
				return fmt.Errorf("error marshaling error JSON: %w", err)
			}
			deleted, derr := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
				ID:       resource.ID,
				Column1:  true,
				State:    sqlc.DeltaResourceStateDeleted, // soft delete (to be hard deleted later by a cleanup maintenance worker)
				Column3:  true,
				SyncedAt: &now,
				Column5:  true,
				Column6:  errorData,
			})
			if derr != nil {
				// handle no rows (since it's possible the delta resource record was deleted during the Work() call)
				if errors.Is(derr, pgx.ErrNoRows) {
					return river.JobCancel(fmt.Errorf("resource %s:%s no longer exists: %w", resource.Object.Kind(), resource.Object.ID(), err))
				}
				return fmt.Errorf("failed to set resource state to deleted: %w", derr)
			}

			deletedRow := toResourceRow(deleted)
			client.eventCh <- []Event{
				{
					Resource:      &deletedRow,
					EventCategory: EventCategoryObjectDeleted,
					Timestamp:     time.Now(),
				},
			}

			return river.JobCancel(fmt.Errorf("resource deleted: %w", err))
		}

		state := sqlc.DeltaResourceStateFailed
		if job.Attempt >= job.MaxAttempts {
			state = sqlc.DeltaResourceStateDegraded
		}

		logger.WarnContext(ctx, "resource failed", "attempt", resourceRow.Attempt, "state", state, "error", err)

		now := time.Now().UTC()
		errorData, err := json.Marshal(deltatype.AttemptError{
			At:      now,
			Attempt: resourceRow.Attempt,
			Error:   err.Error(),
		})
		if err != nil {
			return fmt.Errorf("error marshaling error JSON: %w", err)
		}

		failed, uerr := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
			ID:       resource.ID,
			Column1:  true,
			State:    state,
			Column3:  true,
			SyncedAt: nil,
			Column5:  true,
			Column6:  errorData,
		})
		if uerr != nil {
			// handle no rows (since it's possible the delta resource record was deleted during the Work() call)
			if errors.Is(uerr, pgx.ErrNoRows) {
				return river.JobCancel(fmt.Errorf("resource %s:%s no longer exists: %w", resource.Object.Kind(), resource.Object.ID(), err))
			}
			return uerr
		}

		failedRow := toResourceRow(failed)
		client.eventCh <- []Event{
			{
				Resource:      &failedRow,
				EventCategory: EventCategoryObjectFailed,
				Timestamp:     time.Now(),
			},
		}

		return err
	}

	now := time.Now()
	synced, err := queries.ResourceSetState(ctx, &sqlc.ResourceSetStateParams{
		ID:       resource.ID,
		Column1:  true,
		State:    sqlc.DeltaResourceStateSynced,
		Column3:  true,
		SyncedAt: &now,
	})
	if err != nil {
		// handle no rows (since it's possible the delta resource record was deleted during the Work() call)
		if errors.Is(err, pgx.ErrNoRows) {
			return river.JobCancel(fmt.Errorf("resource %s:%s no longer exists: %w", resource.Object.Kind(), resource.Object.ID(), err))
		}
		return err
	}

	syncedRow := toResourceRow(synced)
	client.eventCh <- []Event{
		{
			Resource:      &syncedRow,
			EventCategory: EventCategoryObjectSynced,
			Timestamp:     time.Now(),
		},
	}

	logger.InfoContext(ctx, "finished working resource", "id", resource.ID, "resource_id", resource.Object.ID(), "resource_kind", resource.Object.Kind())

	return nil
}

func (w *controllerWorker[T]) Timeout(job *river.Job[Resource[T]]) time.Duration {
	// we enforce our own timeout so we want to remove River's underlying timeout on the job
	return -1
}
