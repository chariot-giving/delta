package delta

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig_validateReconcileRetention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		config            Config
		reconcileInterval time.Duration
		wantErrContains   string
	}{
		{
			name: "no namespaces, no error",
			config: Config{
				Namespaces: nil,
			},
			reconcileInterval: time.Hour,
		},
		{
			name: "interval shorter than ResourceExpiry, ok",
			config: Config{
				Namespaces: map[string]NamespaceConfig{
					"events": {ResourceExpiry: 24 * time.Hour},
				},
			},
			reconcileInterval: 12 * time.Hour,
		},
		{
			name: "interval equal to ResourceExpiry, error",
			config: Config{
				Namespaces: map[string]NamespaceConfig{
					"events": {ResourceExpiry: 12 * time.Hour},
				},
			},
			reconcileInterval: 12 * time.Hour,
			wantErrContains:   "ResourceExpiry",
		},
		{
			name: "interval longer than ResourceExpiry, error",
			config: Config{
				Namespaces: map[string]NamespaceConfig{
					"events": {ResourceExpiry: 6 * time.Hour},
				},
			},
			reconcileInterval: 12 * time.Hour,
			wantErrContains:   "ResourceExpiry",
		},
		{
			name: "interval shorter than namespace SyncedResourceRetentionPeriod, ok",
			config: Config{
				Namespaces: map[string]NamespaceConfig{
					"events": {SyncedResourceRetentionPeriod: 7 * 24 * time.Hour},
				},
			},
			reconcileInterval: 24 * time.Hour,
		},
		{
			name: "interval longer than namespace SyncedResourceRetentionPeriod, error",
			config: Config{
				Namespaces: map[string]NamespaceConfig{
					"events": {SyncedResourceRetentionPeriod: 12 * time.Hour},
				},
			},
			reconcileInterval: 24 * time.Hour,
			wantErrContains:   "SyncedResourceRetentionPeriod",
		},
		{
			name: "interval longer than client default SyncedResourceRetentionPeriod, error",
			config: Config{
				SyncedResourceRetentionPeriod: 12 * time.Hour,
				Namespaces: map[string]NamespaceConfig{
					"events": {},
				},
			},
			reconcileInterval: 24 * time.Hour,
			wantErrContains:   "SyncedResourceRetentionPeriod",
		},
		{
			name: "ResourceExpiry shadows SyncedResourceRetentionPeriod check",
			config: Config{
				SyncedResourceRetentionPeriod: 12 * time.Hour,
				Namespaces: map[string]NamespaceConfig{
					// ResourceExpiry causes synced rows to be re-worked, not
					// hard-deleted, so SyncedResourceRetentionPeriod doesn't
					// apply. The ResourceExpiry check still does.
					"events": {ResourceExpiry: 48 * time.Hour},
				},
			},
			reconcileInterval: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.validateReconcileRetention("kind1", tt.reconcileInterval)
			if tt.wantErrContains == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErrContains)
			}
		})
	}
}
