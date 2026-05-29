package delta

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

const namespaceDefault = "default"

// NamespaceConfig contains namespace-specific configuration.
type NamespaceConfig struct {
	// MaxWorkers is the maximum number of workers to run for the namespace, or put
	// otherwise, the maximum parallelism to run.
	//
	// This is the maximum number of workers within this particular client
	// instance, but note that it doesn't control the total number of workers
	// across parallel processes. Installations will want to calculate their
	// total number by multiplying this number by the number of parallel nodes
	// running River clients configured to the same database and queue.
	//
	// Requires a minimum of 1, and a maximum of 10,000.
	MaxWorkers int

	// ResourceExpiry is the duration (seconds) after which a previously
	// synced resource is considered expired.
	//
	// This can be used to re-sync resources that may change or update in the
	// external system that may not be automatically re-informed based on a
	// creation time After filter. For immutable resources, this should NOT be set
	// to prevent unnecessary work for objects that will never change.
	//
	// If this is set to 0, resources within the namespace will never expire.
	// Defaults to 0 (resources never expire).
	ResourceExpiry time.Duration

	// DeletedResourceRetentionPeriod is the amount of time to keep deleted resources
	// around before they're removed permanently.
	//
	// Defaults to 24 hours.
	DeletedResourceRetentionPeriod time.Duration

	// SyncedResourceRetentionPeriod is the amount of time to keep synced resources
	// around before they're removed permanently.
	// This value is only respected if ResourceExpiry is set to a zero value.
	//
	// Defaults to 24 hours.
	SyncedResourceRetentionPeriod time.Duration

	// DegradedResourceRetentionPeriod is the amount of time to keep degraded resources
	// around before they're removed permanently.
	//
	// Defaults to 24 hours.
	DegradedResourceRetentionPeriod time.Duration
}

// Alphanumeric characters separated by underscores or hyphens.
// Using a character class with '|' was incorrect and allowed pipe
// characters. This regex properly restricts input to letters,
// numbers, underscores and hyphens.
var nameRegex = regexp.MustCompile(`^[a-z0-9]+(?:[_-][a-z0-9]+)*$`)

func validateNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("namespace cannot be empty")
	}
	if len(namespace) > 64 {
		return errors.New("namespace cannot be longer than 64 characters")
	}
	if !nameRegex.MatchString(namespace) {
		return fmt.Errorf("namespace is invalid, expected letters and numbers separated by underscores or hyphens: %q", namespace)
	}
	return nil
}
