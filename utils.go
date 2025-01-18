package delta

import (
	"github.com/chariot-giving/delta/deltatype"
	"github.com/chariot-giving/delta/internal/db/sqlc"
)

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

func toResourceRow(resource *sqlc.DeltaResource) deltatype.ResourceRow {
	return deltatype.ResourceRow{
		ID:            resource.ID,
		ObjectID:      resource.ObjectID,
		ObjectKind:    resource.Kind,
		Namespace:     resource.Namespace,
		EncodedObject: resource.Object,
		Hash:          resource.Hash,
		Metadata:      resource.Metadata,
		CreatedAt:     resource.CreatedAt,
		SyncedAt:      resource.SyncedAt,
		Attempt:       int(resource.Attempt),
		MaxAttempts:   int(resource.MaxAttempts),
		State:         deltatype.ResourceState(resource.State),
		Tags:          resource.Tags,
	}
}
