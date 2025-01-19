package delta

import (
	"context"
	"fmt"
	"time"

	"github.com/chariot-giving/delta/internal/db/sqlc"
	"github.com/chariot-giving/delta/internal/object"
	"github.com/riverqueue/river"
)

type Worker[T Object] interface {
	Work(ctx context.Context, resource *Resource[T]) error
}

type controllerWorker[T Object] struct {
	factory object.ObjectFactory
	river.WorkerDefaults[Resource[T]]
}

func (w *controllerWorker[T]) Work(ctx context.Context, job *river.Job[Resource[T]]) error {
	client, err := ClientFromContextSafely(ctx)
	if err != nil {
		return err
	}

	queries := sqlc.New(client.dbPool)
	resource := job.Args
	object := w.factory.Make(resource.ResourceRow)
	if err := object.UnmarshalResource(); err != nil {
		return fmt.Errorf("failed to unmarshal resource: %w", err)
	}

	// do the work!
	err = object.Work(ctx)
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
