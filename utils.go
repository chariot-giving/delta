package delta

// FirstNonZero returns the first argument that is non-zero, or the zero value if
// all are zero.
func firstNonZero[T comparable](values ...T) T {
	var zero T
	for _, val := range values {
		if val != zero {
			return val
		}
	}
	return zero
}
