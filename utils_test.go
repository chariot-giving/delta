package delta

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFirstNonZero(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, firstNonZero(0, 1))
	require.Equal(t, 5, firstNonZero(5, 1))

	require.Equal(t, "default", firstNonZero("", "default"))
	require.Equal(t, "hello", firstNonZero("hello", "default"))

	nonNilConfig := &Config{MaintenanceJobInterval: 5 * time.Second}
	require.Equal(t, nonNilConfig, firstNonZero(nil, nonNilConfig))
}
