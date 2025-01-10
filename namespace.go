package delta

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// NamespaceBundle is a bundle for adding additional namespaces. It's made accessible
// through Client.Namespaces.
type NamespaceBundle struct {
	// Function that adds a manager to the associated client.
	addManager func(namespace string, queueConfig NamespaceConfig) *manager

	fetchCtx context.Context //nolint:containedctx

	// Mutex that's acquired when client is starting and stopping and when a
	// queue is being added so that we can be sure that a client is fully
	// stopped or fully started when adding a new queue.
	startStopMu sync.Mutex

	workCtx context.Context //nolint:containedctx
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
