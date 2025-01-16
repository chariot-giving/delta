package deltatype

import "context"

// ObjectInformMiddleware provides an interface for middleware that integrations can
// use to encapsulate common logic around object informing.
//
// Implementations should embed delta.ObjectMiddlewareDefaults to inherit default
// implementations for phases where no custom code is needed, and for forward
// compatibility in case new functions are added to this interface.
type ObjectInformMiddleware interface {
	// InformMany is invoked around a batch inform operation. Implementations
	// must always include a call to doInner to call down the middleware stack
	// and perform the batch inform, and may run custom code before and after.
	//
	// Returning an error from this function will fail the overarching insert
	// operation, even if the inner inform originally succeeded.
	InformMany(ctx context.Context, manyParams []*ObjectInformParams, doInner func(context.Context) ([]*ObjectInformResult, error)) ([]*ObjectInformResult, error)
}
