// Package deltariver provides a Delta queue implementation for River.
//
// This is currently the only supported driver for Delta and will therefore be
// used by all projects using Delta, but the code is organized this way so that
// other database packages can be supported in future Delta versions.
package deltariver

import (
	"context"

	"github.com/chariot-giving/delta/deltaqueue"
	"github.com/riverqueue/river"
)

type Queue[TTx any] struct {
	client *river.Client[TTx]
}

// TODO: this is not quite right.. if we pass a river client then it needs to be fully initialized with workers
// by the caller so that exposing Start/Stop/AddWorker here doesn't make sense.
// This needs to fully abstract the river client so that the caller doesn't need to know about it.
// If we're doing that, then will we need to run river migrations + DB setup tables in the delta schema?
func New[TTx any](client *river.Client[TTx]) *Queue[TTx] {
	return &Queue[TTx]{client: client}
}

func (q *Queue[TTx]) Start(ctx context.Context) error {
	return q.client.Start(ctx)
}

func (q *Queue[TTx]) Stop(ctx context.Context) error {
	return q.client.Stop(ctx)
}

func (q *Queue[TTx]) AddWorker(worker deltaqueue.Worker) {

}

func (q *Queue[TTx]) Insert(ctx context.Context, args deltaqueue.JobArgs) (*deltaqueue.JobInsertResult, error) {
	row, err := q.client.Insert(ctx, args, nil)
	if err != nil {
		return nil, err
	}

	return &deltaqueue.JobInsertResult{Job: row}, nil
}

func (q *Queue[TTx]) InsertTx(ctx context.Context, tx TTx, args deltaqueue.JobArgs) (*deltaqueue.JobInsertResult, error) {
	row, err := q.client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, err
	}

	return &deltaqueue.JobInsertResult{Job: row}, nil
}
