// Package deltariver provides a Delta queue implementation for River.
//
// This is currently the only supported driver for Delta and will therefore be
// used by all projects using Delta, but the code is organized this way so that
// other database packages can be supported in future Delta versions.
package deltariver

import (
	"context"

	"github.com/chariot-giving/delta/deltaqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type Queue struct {
	dbPool  *pgxpool.Pool
	workers *river.Workers
	client  *river.Client[pgx.Tx]
}

func New(dbPool *pgxpool.Pool) *Queue {
	workers := river.NewWorkers()
	return &Queue{
		dbPool:  dbPool,
		workers: workers,
	}
}

func (q *Queue) Start(ctx context.Context) error {
	config := &river.Config{
		Workers: q.workers,
	}
	client, err := river.NewClient(riverpgxv5.New(q.dbPool), config)
	if err != nil {
		return err
	}
	q.client = client

	return q.client.Start(ctx)
}

func (q *Queue) Stop(ctx context.Context) error {
	if q.client == nil {
		return nil
	}
	return q.client.Stop(ctx)
}

func (q *Queue) AddWorker(worker deltaqueue.Worker) {
	// TODO: wrap the worker in a river worker
	river.AddWorker(q.workers, worker)
}

func (q *Queue) Insert(ctx context.Context, args deltaqueue.JobArgs) (*deltaqueue.JobInsertResult, error) {
	row, err := q.client.Insert(ctx, args, nil)
	if err != nil {
		return nil, err
	}

	return &deltaqueue.JobInsertResult{
		Job:                      &riverJob{row: row.Job},
		UniqueSkippedAsDuplicate: row.UniqueSkippedAsDuplicate,
	}, nil
}

func (q *Queue) InsertTx(ctx context.Context, tx pgx.Tx, args deltaqueue.JobArgs) (*deltaqueue.JobInsertResult, error) {
	row, err := q.client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return nil, err
	}

	return &deltaqueue.JobInsertResult{
		Job:                      &riverJob{row: row.Job},
		UniqueSkippedAsDuplicate: row.UniqueSkippedAsDuplicate,
	}, nil
}
