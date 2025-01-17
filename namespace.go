package delta

import (
	"errors"
	"fmt"
	"regexp"
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
	// ExpiryDuration is the duration (seconds) after which a resource is considered expired.
	//
	// If this is set to 0, resources within the namespace will never expire.
	ResourceExpiry int
}

var nameRegex = regexp.MustCompile(`^(?:[a-z0-9])+(?:[_|\-]?[a-z0-9]+)*$`)

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
