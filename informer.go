package delta

import "context"

// Informer is an interface that provides a way to inform the controller of resources.
type Informer[T Object] interface {
	// Inform informs the controller of resources.
	Inform(ctx context.Context, queue chan T)
}
