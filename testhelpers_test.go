package delta

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"

	"github.com/chariot-giving/delta/deltatype"
)

// testDatabaseURLEnv names the environment variable holding the Postgres URL
// used by database-backed tests. Tests that need a database are skipped when
// it's unset so `go test ./...` keeps working without Postgres.
const testDatabaseURLEnv = "DELTA_TEST_DATABASE_URL"

// testPool returns a pool scoped to a fresh, randomly named schema with the
// delta and River migrations applied. The schema is dropped when the test ends.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv(testDatabaseURLEnv)
	if databaseURL == "" {
		t.Skipf("%s not set; skipping database test", testDatabaseURLEnv)
	}

	ctx := context.Background()

	suffix := make([]byte, 6)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	schema := "delta_test_" + hex.EncodeToString(suffix)

	adminConn, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	_, err = adminConn.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	require.NoError(t, adminConn.Close(ctx))

	config, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 20

	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
		conn, err := pgx.Connect(context.Background(), databaseURL)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	migrateDelta(t, pool)

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	return pool
}

// migrateDelta applies the "Up" section of each goose migration. Statements are
// executed one at a time because some (e.g. DROP INDEX CONCURRENTLY) can't run
// inside the implicit transaction of a multi-statement query.
func migrateDelta(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("internal", "db", "migrations", "*.sql"))
	require.NoError(t, err)
	sort.Strings(files)

	for _, file := range files {
		contents, err := os.ReadFile(file)
		require.NoError(t, err)

		up, _, _ := strings.Cut(string(contents), "-- +goose Down")
		for _, statement := range splitSQLStatements(up) {
			_, err := pool.Exec(context.Background(), statement)
			require.NoError(t, err, "migration %s: %s", file, statement)
		}
	}
}

func splitSQLStatements(sql string) []string {
	var (
		statements []string
		current    strings.Builder
	)
	for line := range strings.SplitSeq(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			statements = append(statements, current.String())
			current.Reset()
		}
	}
	return statements
}

// testObject is a minimal Object used by database-backed tests.
type testObject struct {
	ObjectID string `json:"id"`
	Value    string `json:"value"`
}

func (o testObject) ID() string   { return o.ObjectID }
func (o testObject) Kind() string { return "test_object" }

// testController is a configurable Controller for testObject. Work delegates to
// workFunc (success when nil) and Inform emits the objects in informObjects.
type testController struct {
	InformerDefaults[testObject]
	WorkerDefaults[testObject]

	mu            sync.Mutex
	informObjects []testObject
	workFunc      func(ctx context.Context, resource *Resource[testObject]) error
	workCalls     map[string]int
}

func (c *testController) Inform(ctx context.Context, opts *InformOptions) (<-chan testObject, error) {
	c.mu.Lock()
	objects := append([]testObject(nil), c.informObjects...)
	c.mu.Unlock()

	queue := make(chan testObject)
	go func() {
		defer close(queue)
		for _, obj := range objects {
			select {
			case queue <- obj:
			case <-ctx.Done():
				return
			}
		}
	}()
	return queue, nil
}

func (c *testController) Work(ctx context.Context, resource *Resource[testObject]) error {
	c.mu.Lock()
	if c.workCalls == nil {
		c.workCalls = make(map[string]int)
	}
	c.workCalls[resource.Object.ID()]++
	workFunc := c.workFunc
	c.mu.Unlock()

	if workFunc != nil {
		return workFunc(ctx, resource)
	}
	return nil
}

func (c *testController) WorkCalls(objectID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workCalls[objectID]
}

// testClient builds (but doesn't start) a Client managing the "default"
// namespace with controller registered. Maintenance and informer schedules are
// pushed far out so tests drive them explicitly.
//
//nolint:unparam // mutate is optional per-test config
func testClient(t *testing.T, pool *pgxpool.Pool, controller *testController, mutate ...func(*Config)) *Client {
	t.Helper()

	controllers := NewControllers()
	AddController(controllers, controller)

	config := &Config{
		Logger:                  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Controllers:             controllers,
		Namespaces:              map[string]NamespaceConfig{namespaceDefault: {}},
		MaintenanceJobInterval:  time.Hour,
		ResourceCleanerInterval: time.Hour,
		ResourceInformInterval:  -1,
		ResourceInformJitter:    -1,
	}
	for _, m := range mutate {
		m(config)
	}

	client, err := NewClient(pool, config)
	require.NoError(t, err)
	return client
}

// startClient starts client and stops it when the test ends.
func startClient(t *testing.T, client *Client) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, client.Start(ctx))
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
		cancel()
	})
}

// resourceState returns the state of the resource for objectID.
func resourceState(t *testing.T, pool *pgxpool.Pool, objectID string) deltatype.ResourceState {
	t.Helper()

	var state deltatype.ResourceState
	err := pool.QueryRow(context.Background(),
		"SELECT state::text FROM delta_resource WHERE object_id = $1 AND kind = 'test_object'", objectID).Scan(&state)
	require.NoError(t, err)
	return state
}

// countResourceJobs returns the number of River resource jobs for resource id
// in any of states (all states when empty).
func countResourceJobs(t *testing.T, pool *pgxpool.Pool, resourceID int64, states ...string) int {
	t.Helper()

	query := "SELECT count(*) FROM river_job WHERE kind = 'delta.resource.test_object' AND args->>'ID' = $1"
	args := []any{strconv.FormatInt(resourceID, 10)}
	if len(states) > 0 {
		query += " AND state::text = ANY($2)"
		args = append(args, states)
	}

	var count int
	require.NoError(t, pool.QueryRow(context.Background(), query, args...).Scan(&count))
	return count
}

// eventually polls cond until it returns true or the timeout elapses.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 10*time.Second, 20*time.Millisecond)
}

//nolint:gochecknoglobals
var deltatypeResourceRowForTest = deltatype.ResourceRow{ID: 999_999, ObjectID: "missing", ObjectKind: "test_object"}
