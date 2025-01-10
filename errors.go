package delta

// UnknownResourceKindError is returned when a Client fetches and attempts to
// inform a resource that has not been registered on the Client's Controller bundle (using
// AddController).
type UnknownResourceKindError struct {
	// Kind is the string that was returned by the Object Kind method.
	Kind string
}

// Error returns the error string.
func (e *UnknownResourceKindError) Error() string {
	return "resource kind is not registered in the client's Controller bundle: " + e.Kind
}

// Is implements the interface used by errors.Is to determine if errors are
// equivalent. It returns true for any other UnknownResourceKindError without
// regard to the Kind string so it is possible to detect this type of error
// with:
//
//	errors.Is(err, &UnknownResourceKindError{})
func (e *UnknownResourceKindError) Is(target error) bool {
	_, ok := target.(*UnknownResourceKindError)
	return ok
}
