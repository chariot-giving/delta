// Package deltadriver exposes generic constructs to be implemented by specific
// drivers that wrap third party database packages, with the aim being to keep
// the main Delta interface decoupled from a specific database package so that
// other packages or other major versions of packages can be supported in future
// Delta versions.
//
// Delta currently only supports Pgx v5, and the interface here wrap it with
// only the thinnest possible layer. Adding support for alternate packages will
// require the interface to change substantially, and therefore it should not be
// implemented or invoked by user code. Changes to interfaces in this package
// WILL NOT be considered breaking changes for purposes of Delta's semantic
// versioning.
package deltadriver

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/chariot-giving/delta/deltatype"
)

var (
	ErrClosedPool     = errors.New("underlying driver pool is closed")
	ErrNotImplemented = errors.New("driver does not implement this functionality")
)

// Driver provides a database driver for use with delta.Client.
//
// Its purpose is to wrap the interface of a third party database package, with
// the aim being to keep the main Delta interface decoupled from a specific
// database package so that other packages or major versions of packages can be
// supported in future Delta versions.
//
// Delta currently only supports Pgx v5, and this interface wraps it with only
// the thinnest possible layer. Adding support for alternate packages will
// require it to change substantially, and therefore it should not be
// implemented or invoked by user code. Changes to this interface WILL NOT be
// considered breaking changes for purposes of Delta's semantic versioning.
//
// API is not stable. DO NOT IMPLEMENT.
type Driver[TTx any] interface {
	// GetExecutor gets an executor for the driver.
	//
	// API is not stable. DO NOT USE.
	GetExecutor() Executor

	// GetListener gets a listener for purposes of receiving notifications.
	//
	// API is not stable. DO NOT USE.
	GetListener() Listener

	// GetMigrationFS gets a filesystem containing migrations for the driver.
	//
	// Each set of migration files is expected to exist within the filesystem as
	// `migration/<line>/`. For example:
	//
	//     migration/main/001_create_river_migration.up.sql
	//
	// API is not stable. DO NOT USE.
	GetMigrationFS(line string) fs.FS

	// GetMigrationLines gets supported migration lines from the driver. Most
	// drivers will only support a single line: MigrationLineMain.
	//
	// API is not stable. DO NOT USE.
	GetMigrationLines() []string

	// HasPool returns true if the driver is configured with a database pool.
	//
	// API is not stable. DO NOT USE.
	HasPool() bool

	// SupportsListener gets whether this driver supports a listener. Drivers
	// that don't support a listener support poll only mode only.
	//
	// API is not stable. DO NOT USE.
	SupportsListener() bool

	// UnwrapExecutor gets an executor from a driver transaction.
	//
	// API is not stable. DO NOT USE.
	UnwrapExecutor(tx TTx) ExecutorTx
}

// Executor provides Delta operations against a database. It may be a database
// pool or transaction.
//
// API is not stable. DO NOT IMPLEMENT.
type Executor interface {
	// Begin begins a new subtransaction. ErrSubTxNotSupported may be returned
	// if the executor is a transaction and the driver doesn't support
	// subtransactions (like riverdriver/riverdatabasesql for database/sql).
	Begin(ctx context.Context) (ExecutorTx, error)

	// TableExists checks whether a table exists for the schema in the current
	// search schema.
	TableExists(ctx context.Context, tableName string) (bool, error)

	// ColumnExists checks whether a column for a particular table exists for
	// the schema in the current search schema.
	ColumnExists(ctx context.Context, tableName, columnName string) (bool, error)

	// Exec executes raw SQL. Used for migrations.
	Exec(ctx context.Context, sql string) (struct{}, error)

	NamespaceCreateOrSetUpdated(ctx context.Context, params *NamespaceCreateOrSetUpdatedAtParams) error
	NamespaceGet(ctx context.Context, namespace string) (*deltatype.Namespace, error)
	NamespaceList(ctx context.Context, limit int) ([]*deltatype.Namespace, error)

	ResourceGetByID(ctx context.Context, id string) (*deltatype.ResourceRow, error)
	ResourceGetUnknown(ctx context.Context, params *ResourceGetUnknownParams) ([]*deltatype.ResourceRow, error)
	ResourceGetByKindMany(ctx context.Context, kind []string) ([]*deltatype.ResourceRow, error)
	ResourceUpdate(ctx context.Context, resource *ResourceUpdateParams) error
}

// ExecutorTx is an executor which is a transaction. In addition to standard
// Executor operations, it may be committed or rolled back.
//
// API is not stable. DO NOT IMPLEMENT.
type ExecutorTx interface {
	Executor

	// Commit commits the transaction.
	//
	// API is not stable. DO NOT USE.
	Commit(ctx context.Context) error

	// Rollback rolls back the transaction.
	//
	// API is not stable. DO NOT USE.
	Rollback(ctx context.Context) error
}

// Listener listens for notifications. In Postgres, this is a database
// connection where `LISTEN` has been run.
//
// API is not stable. DO NOT IMPLEMENT.
type Listener interface {
	Close(ctx context.Context) error
	Connect(ctx context.Context) error
	Listen(ctx context.Context, topic string) error
	Ping(ctx context.Context) error
	Unlisten(ctx context.Context, topic string) error
	WaitForNotification(ctx context.Context) (*Notification, error)
}

type Notification struct {
	Payload string
	Topic   string
}

type ResourceGetUnknownParams struct {
	AttemptedBy string
	Max         int
	Now         *time.Time
	Namespace   string
}

type ResourceUpdateParams struct {
	ID               int64
	ErrorsDoUpdate   bool
	Errors           [][]byte
	SyncedAtDoUpdate bool
	SyncedAt         *time.Time
	StateDoUpdate    bool
	State            deltatype.ResourceState
}

type NamespaceCreateOrSetUpdatedAtParams struct {
	Metadata       []byte
	Name           string
	PausedAt       *time.Time
	UpdatedAt      *time.Time
	ExpiryDuration *time.Duration
}
