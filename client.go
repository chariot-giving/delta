package delta

import (
	"context"
	"log/slog"
)

// Config is the configuration for a Client.
type Config struct {
	// ID is the unique identifier for this client. If not set, a random
	// identifier will be generated.
	//
	// This is used to identify the client in job attempts and for leader election.
	// This value must be unique across all clients in the same database and
	// schema and there must not be more than one process running with the same
	// ID at the same time.
	//
	// A client ID should differ between different programs and must be unique
	// across all clients in the same database and schema. There must not be
	// more than one process running with the same ID at the same time.
	// Duplicate IDs between processes will lead to facilities like leader
	// election or client statistics to fail in novel ways. However, the client
	// ID is shared by all executors within any given client. (i.e.  different
	// Go processes have different IDs, but IDs are shared within any given
	// process.)
	//
	// If in doubt, leave this property empty.
	ID string

	// Logger is the structured logger to use for logging purposes. If none is
	// specified, logs will be emitted to STDOUT with messages at warn level
	// or higher.
	Logger *slog.Logger

	// Controllers is a bundle of registered resource controllers.
	//
	// This field may be omitted for a program that's only informing resources
	// rather than working them, but if it is configured the client can validate
	// ahead of time that a controller is properly registered for an inserted resource.
	// (i.e.  That it wasn't forgotten by accident.)
	Controllers Controllers

	// StreamSchedule?

	// RetryPolicy?
}

// Client is a single isolated instance of Delta. Your application may use
// multiple instances operating on different databases or Postgres schemas
// within a single database.
type Client[TTx any] struct {
	config  *Config
	stopped <-chan struct{}
	// workCancel cancels the context used for all work goroutines. Normal Stop
	// does not cancel that context.
	workCancel context.CancelCauseFunc
}
