package delta

import (
	"context"
)

type Worker[T Object] interface {
	Work(ctx context.Context, resource *Resource[T]) error
}
