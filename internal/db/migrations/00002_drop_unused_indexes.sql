-- +goose NO TRANSACTION
-- +goose Up
-- Drop indexes on delta_resource that have no query coverage. They inflate the
-- table footprint and write amplification with zero read benefit (confirmed via
-- prod pg_stat_user_indexes: zero idx_scan across millions of pkey scans).
--
--   * The two GIN indexes account for ~91% of the table's on-disk size. No query
--     filters on the `object` or `metadata` jsonb columns.
--   * delta_resource_object_id is redundant: object_id is the leading column of
--     the delta_resource_object_id_kind_unique composite, and no query filters
--     object_id without kind.
--   * external_created_at is never used as a filter or sort key.
--
-- CONCURRENTLY so the drops don't take an exclusive lock on the table; this
-- requires the migration to run outside a transaction (see NO TRANSACTION above).
DROP INDEX CONCURRENTLY IF EXISTS delta_resource_object_index;
DROP INDEX CONCURRENTLY IF EXISTS delta_resource_metadata_index;
DROP INDEX CONCURRENTLY IF EXISTS delta_resource_object_id;
DROP INDEX CONCURRENTLY IF EXISTS delta_resource_external_created_at;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS delta_resource_external_created_at ON delta_resource USING btree(external_created_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS delta_resource_object_id ON delta_resource USING btree(object_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS delta_resource_metadata_index ON delta_resource USING GIN(metadata);
CREATE INDEX CONCURRENTLY IF NOT EXISTS delta_resource_object_index ON delta_resource USING GIN(object);
