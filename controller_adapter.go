package delta

import (
	"context"

	"github.com/riverqueue/river"
)

// First, create a non-generic interface for internal use
type controllerInterface[T Object] interface {
	Work(ctx context.Context, job *river.Job[Resource[T]]) error
	Inform(ctx context.Context, opts *InformOptions) (<-chan Object, error)
}

// Add an adapter to bridge between generic and non-generic
type controllerAdapter[T Object] struct {
	controller Controller[T]
}

func (a *controllerAdapter[T]) Work(ctx context.Context, job *river.Job[Resource[T]]) error {
	return a.controller.Work(ctx, &job.Args)
}

func (a *controllerAdapter[T]) Inform(ctx context.Context, opts *InformOptions) (<-chan Object, error) {
	// Create a typed channel
	queue := make(chan Object)

	typedQueue, err := a.controller.Inform(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Start a goroutine to convert from typed to untyped channel
	go func() {
		defer close(queue)
		for obj := range typedQueue {
			queue <- obj
		}
	}()

	return queue, nil
}
