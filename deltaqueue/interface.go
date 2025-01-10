package deltaqueue

import "context"

// Queue provides a job queue for use with delta.Client.
//
// It's purpose is to wrap the interface of a third party queue package, with
// the aim being to keep the main Delta interface decoupled from a specific
// queue package so that other packages or major versions of packages can be
// supported in future Delta versions.
//
// The Queue implementation is responsible for coordinating job execution
// across multiple instances of the same Delta client and therefore should
// provide a way to ensure reliable QoS guarantees and run maintenance tasks.
// This likely means the Queue implementation should have some form of leader
// election or consensus mechanism.
//
// Delta currently only supports River, and this interface wraps it with only
// the thinnest possible layer. Adding support for alternate packages will
// require it to change substantially, and therefore it should not be
// implemented or invoked by user code. Changes to this interface WILL NOT be
// considered breaking changes for purposes of Delta's semantic versioning.
//
// API is not stable. DO NOT IMPLEMENT.
type Queue[TTx any] interface {
	// Start starts the job queue.
	Start(ctx context.Context) error
	// Stop stops the job queue.
	Stop(ctx context.Context) error
	// AddWorker adds a worker to the job queue.
	AddWorker(worker Worker)
	// Insert inserts a new job with the provided args.
	Insert(ctx context.Context, args JobArgs) (*JobInsertResult, error)
	// InsertTx inserts a new job with the provided args on the given transaction.
	InsertTx(ctx context.Context, tx TTx, args JobArgs) (*JobInsertResult, error)
}

type Worker interface {
	// Work processes a job.
	Work(ctx context.Context, job Job) error
}

type JobArgs interface {
	// Name returns the job's name.
	Kind() string
}

type Job interface {
	// ID returns the job's unique identifier.
	ID() string
	// Name returns the job's name.
	Kind() string
	// Args returns the job's arguments.
	Args() JobArgs
}

type JobInsertResult struct {
	// Job is the job that was inserted.
	// Nil if the job was skipped.
	Job Job

	// UniqueSkippedAsDuplicate is true if for a unique job, the insertion was
	// skipped due to an equivalent job matching unique property already being
	// present.
	UniqueSkippedAsDuplicate bool
}
