package delta

import (
	"context"
)

// First, create a non-generic interface for internal use
type controllerInterface interface {
	Inform(ctx context.Context, queue chan Object, opts *InformOptions) error
}

// Add an adapter to bridge between generic and non-generic
type controllerAdapter[T Object] struct {
	controller Controller[T]
}

func (a *controllerAdapter[T]) Inform(ctx context.Context, queue chan Object, opts *InformOptions) error {
	// Create a typed channel
	typedQueue := make(chan T)

	// Start a goroutine to convert from typed to untyped channel
	go func() {
		defer close(queue)
		for obj := range typedQueue {
			queue <- obj
		}
	}()

	return a.controller.Inform(ctx, typedQueue, opts)
}
