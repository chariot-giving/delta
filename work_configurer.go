package delta

import "github.com/riverqueue/river"

// Non-generic interface implemented by WorkConfigurer below.
type workConfigurerInterface interface {
	Configure(workers *river.Workers)
}

// Contains a generic job args param to allow it to call AddWorker with a type
// parameter.
type workConfigurer[T Object] struct {
	worker   river.Worker[Resource[T]]
	informer river.Worker[InformArgs[T]]
}

func (c *workConfigurer[T]) Configure(workers *river.Workers) {
	river.AddWorker(workers, c.worker)
	river.AddWorker(workers, c.informer)
}
