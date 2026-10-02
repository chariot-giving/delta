# Operating Delta

Runbook notes for running Delta in production. See also
[monitoring.md](monitoring.md).

## Sizing the database pool

Resource workers hold a pool connection, inside a transaction, for the whole
duration of `Work()`. River shares the same pool for fetching, completing and
leader election. Size the pool to at least:

```
sum(Parallelism of every controller) + 3 (controller queue) + 1 (maintenance) + 3 (River overhead)
```

`NewClient` logs `delta: database pool is smaller than the configured worker
parallelism` when the pool is smaller than this. In production, bank ran about
24 controllers on a pool of 5 connections, and River logged hundreds of fetch,
completer and leader-election timeouts during load.

Also set a `statement_timeout` on Delta's pool (production currently uses `0`)
so one stuck query can't hold a connection forever.

## River job table bloat

River owns the `river_job` table in Delta's schema. Its `args` and `metadata`
columns have GIN indexes. GIN indexes don't shrink after deletes, and with
about 30k resource jobs churned per day they bloat. In production
`river_job_args_index` was 1.28 GB against about 40 MB of table data.

River ships a daily reindexer for exactly this, but in production it fails
every night with:

```
maintenance.Reindexer: Error reindexing ... must be owner of index river_job_* (SQLSTATE 42501)
```

Reindexing requires owning the index, and indexes are owned by their table's
owner. Fix it in one of two ways:

- Make the application role own River's tables in the `delta` schema
  (preferred, because the nightly reindexer then keeps the indexes compact):

  ```sql
  ALTER TABLE delta.river_job OWNER TO <app_role>;
  ```

- Or reindex manually as the current owner when the index gets large:

  ```sql
  REINDEX INDEX CONCURRENTLY delta.river_job_args_index;
  REINDEX INDEX CONCURRENTLY delta.river_job_metadata_index;
  ```

To check the current sizes:

```sql
SELECT indexrelname, pg_size_pretty(pg_relation_size(indexrelid)) AS size, idx_scan
FROM pg_stat_user_indexes
WHERE schemaname = 'delta' AND relname = 'river_job'
ORDER BY pg_relation_size(indexrelid) DESC;
```

Don't drop these indexes from Delta's migrations, because River's own
migrations manage them. River's job cleaner already prunes finalized jobs on
schedule (completed and cancelled after 24h, discarded after 7d), so pruning
more aggressively won't reduce the index size. Only reindexing does.

## Poison resources

A resource whose `Work()` keeps failing retries up to its `MaxAttempts`
(`InformOpts.MaxAttempts`, default 10, with River's exponential backoff), then
moves to `degraded` and is hard-deleted after `DegradedResourceRetentionPeriod`.
To find resources that are failing now:

```sql
SELECT id, kind, object_id, state, attempt, attempted_at,
       errors[array_length(errors, 1)]->>'error' AS last_error
FROM delta.delta_resource
WHERE state IN ('failed', 'degraded')
ORDER BY attempted_at DESC;
```

Once the underlying problem is fixed, re-inform the object (a changed object
is always re-worked). For an unchanged object that has synced, call
`Client.Invalidate`.

## Follow-up: moving Work() out of the transaction

Holding a transaction across `Work()` is what ties pool size to parallelism.
Removing it safely needs a lease column on `delta_resource` (for example
`leased_until timestamptz`) so that a follow-up job can't run `Work()` for the
same resource concurrently. The worker would:

1. claim the row (set the lease, `attempt + 1`) and commit;
2. run `Work()` without a transaction;
3. write the outcome guarded on its own lease and attempt, and clear the lease.

A job that finds a live lease snoozes instead of cancelling. Because this needs
a migration that consumers apply themselves, ship it with code that checks
whether the column exists and falls back to the current behavior until it does.
