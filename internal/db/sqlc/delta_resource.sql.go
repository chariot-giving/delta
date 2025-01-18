// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.27.0
// source: delta_resource.sql

package sqlc

import (
	"context"
	"time"
)

const resourceCreateOrUpdate = `-- name: ResourceCreateOrUpdate :one
INSERT INTO delta_resource (object_id, kind, namespace, state, created_at, object, metadata, tags, hash, max_attempts)
VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7, $8, $9)
ON CONFLICT (object_id, kind) DO UPDATE
SET state = $4,
    object = $5,
    metadata = $6,
    tags = $7,
    hash = $8,
    max_attempts = $9
RETURNING delta_resource.id, delta_resource.state, delta_resource.attempt, delta_resource.max_attempts, delta_resource.attempted_at, delta_resource.created_at, delta_resource.synced_at, delta_resource.object_id, delta_resource.kind, delta_resource.namespace, delta_resource.object, delta_resource.hash, delta_resource.metadata, delta_resource.tags, delta_resource.errors, 
    (xmax = 0) as is_insert
`

type ResourceCreateOrUpdateParams struct {
	ObjectID    string
	Kind        string
	Namespace   string
	State       DeltaResourceState
	Object      []byte
	Metadata    []byte
	Tags        []string
	Hash        []byte
	MaxAttempts int16
}

type ResourceCreateOrUpdateRow struct {
	DeltaResource DeltaResource
	IsInsert      bool
}

func (q *Queries) ResourceCreateOrUpdate(ctx context.Context, arg *ResourceCreateOrUpdateParams) (*ResourceCreateOrUpdateRow, error) {
	row := q.db.QueryRow(ctx, resourceCreateOrUpdate,
		arg.ObjectID,
		arg.Kind,
		arg.Namespace,
		arg.State,
		arg.Object,
		arg.Metadata,
		arg.Tags,
		arg.Hash,
		arg.MaxAttempts,
	)
	var i ResourceCreateOrUpdateRow
	err := row.Scan(
		&i.DeltaResource.ID,
		&i.DeltaResource.State,
		&i.DeltaResource.Attempt,
		&i.DeltaResource.MaxAttempts,
		&i.DeltaResource.AttemptedAt,
		&i.DeltaResource.CreatedAt,
		&i.DeltaResource.SyncedAt,
		&i.DeltaResource.ObjectID,
		&i.DeltaResource.Kind,
		&i.DeltaResource.Namespace,
		&i.DeltaResource.Object,
		&i.DeltaResource.Hash,
		&i.DeltaResource.Metadata,
		&i.DeltaResource.Tags,
		&i.DeltaResource.Errors,
		&i.IsInsert,
	)
	return &i, err
}

const resourceGetByID = `-- name: ResourceGetByID :one
SELECT id, state, attempt, max_attempts, attempted_at, created_at, synced_at, object_id, kind, namespace, object, hash, metadata, tags, errors
FROM delta_resource
WHERE id = $1
LIMIT 1
`

func (q *Queries) ResourceGetByID(ctx context.Context, id int64) (*DeltaResource, error) {
	row := q.db.QueryRow(ctx, resourceGetByID, id)
	var i DeltaResource
	err := row.Scan(
		&i.ID,
		&i.State,
		&i.Attempt,
		&i.MaxAttempts,
		&i.AttemptedAt,
		&i.CreatedAt,
		&i.SyncedAt,
		&i.ObjectID,
		&i.Kind,
		&i.Namespace,
		&i.Object,
		&i.Hash,
		&i.Metadata,
		&i.Tags,
		&i.Errors,
	)
	return &i, err
}

const resourceGetByObjectIDAndKind = `-- name: ResourceGetByObjectIDAndKind :one
SELECT id, state, attempt, max_attempts, attempted_at, created_at, synced_at, object_id, kind, namespace, object, hash, metadata, tags, errors FROM delta_resource
WHERE object_id = $1
    AND kind = $2
LIMIT 1
`

type ResourceGetByObjectIDAndKindParams struct {
	ObjectID string
	Kind     string
}

func (q *Queries) ResourceGetByObjectIDAndKind(ctx context.Context, arg *ResourceGetByObjectIDAndKindParams) (*DeltaResource, error) {
	row := q.db.QueryRow(ctx, resourceGetByObjectIDAndKind, arg.ObjectID, arg.Kind)
	var i DeltaResource
	err := row.Scan(
		&i.ID,
		&i.State,
		&i.Attempt,
		&i.MaxAttempts,
		&i.AttemptedAt,
		&i.CreatedAt,
		&i.SyncedAt,
		&i.ObjectID,
		&i.Kind,
		&i.Namespace,
		&i.Object,
		&i.Hash,
		&i.Metadata,
		&i.Tags,
		&i.Errors,
	)
	return &i, err
}

const resourceSetState = `-- name: ResourceSetState :one
UPDATE delta_resource
SET state = CASE
        WHEN $1::boolean THEN $2
        ELSE state
    END,
    synced_at = CASE
        WHEN $3::boolean THEN $4
        ELSE synced_at
    END,
    errors = CASE
        WHEN $5::boolean THEN array_append(errors, $6::jsonb)
        ELSE errors
    END
WHERE id = $7
RETURNING id, state, attempt, max_attempts, attempted_at, created_at, synced_at, object_id, kind, namespace, object, hash, metadata, tags, errors
`

type ResourceSetStateParams struct {
	Column1  bool
	State    DeltaResourceState
	Column3  bool
	SyncedAt *time.Time
	Column5  bool
	Column6  []byte
	ID       int64
}

func (q *Queries) ResourceSetState(ctx context.Context, arg *ResourceSetStateParams) (*DeltaResource, error) {
	row := q.db.QueryRow(ctx, resourceSetState,
		arg.Column1,
		arg.State,
		arg.Column3,
		arg.SyncedAt,
		arg.Column5,
		arg.Column6,
		arg.ID,
	)
	var i DeltaResource
	err := row.Scan(
		&i.ID,
		&i.State,
		&i.Attempt,
		&i.MaxAttempts,
		&i.AttemptedAt,
		&i.CreatedAt,
		&i.SyncedAt,
		&i.ObjectID,
		&i.Kind,
		&i.Namespace,
		&i.Object,
		&i.Hash,
		&i.Metadata,
		&i.Tags,
		&i.Errors,
	)
	return &i, err
}

const resourceUpdateAndGetByObjectIDAndKind = `-- name: ResourceUpdateAndGetByObjectIDAndKind :one
WITH locked_resource AS (
    SELECT id, state, attempt, max_attempts, attempted_at, created_at, synced_at, object_id, kind, namespace, object, hash, metadata, tags, errors
    FROM delta_resource dr
    WHERE dr.object_id = $1
        AND dr.kind = $2 FOR
    UPDATE SKIP LOCKED
)
UPDATE delta_resource
SET state = 'pending',
    attempt = delta_resource.attempt + 1,
    attempted_at = NOW()
FROM locked_resource
WHERE delta_resource.id = locked_resource.id
RETURNING delta_resource.id, delta_resource.state, delta_resource.attempt, delta_resource.max_attempts, delta_resource.attempted_at, delta_resource.created_at, delta_resource.synced_at, delta_resource.object_id, delta_resource.kind, delta_resource.namespace, delta_resource.object, delta_resource.hash, delta_resource.metadata, delta_resource.tags, delta_resource.errors
`

type ResourceUpdateAndGetByObjectIDAndKindParams struct {
	ObjectID string
	Kind     string
}

func (q *Queries) ResourceUpdateAndGetByObjectIDAndKind(ctx context.Context, arg *ResourceUpdateAndGetByObjectIDAndKindParams) (*DeltaResource, error) {
	row := q.db.QueryRow(ctx, resourceUpdateAndGetByObjectIDAndKind, arg.ObjectID, arg.Kind)
	var i DeltaResource
	err := row.Scan(
		&i.ID,
		&i.State,
		&i.Attempt,
		&i.MaxAttempts,
		&i.AttemptedAt,
		&i.CreatedAt,
		&i.SyncedAt,
		&i.ObjectID,
		&i.Kind,
		&i.Namespace,
		&i.Object,
		&i.Hash,
		&i.Metadata,
		&i.Tags,
		&i.Errors,
	)
	return &i, err
}
