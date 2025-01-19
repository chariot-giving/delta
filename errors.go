package delta

// ResourceDelete wraps err and can be returned from a Controller's Work method to
// indicate that the resource should be deleted.
func ResourceDelete(err error) error {
	return &ResourceDeleteError{err: err}
}

// ResourceDeleteError is the error type returned by ResourceDelete. It should not be
// initialized directly, but is returned from the [ResourceDelete] function and can
// be used for test assertions.
type ResourceDeleteError struct {
	err error
}

func (e *ResourceDeleteError) Error() string {
	if e.err == nil {
		return "ResourceDeleteError: <nil>"
	}
	return "ResourceDeleteError: " + e.err.Error()
}

func (e *ResourceDeleteError) Is(target error) bool {
	_, ok := target.(*ResourceDeleteError)
	return ok
}

func (e *ResourceDeleteError) Unwrap() error { return e.err }
