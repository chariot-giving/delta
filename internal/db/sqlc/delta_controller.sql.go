// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.27.0
// source: delta_controller.sql

package sqlc

import (
	"context"
	"time"
)

const controllerCreateOrSetUpdatedAt = `-- name: ControllerCreateOrSetUpdatedAt :one
INSERT INTO delta_controller(
    created_at,
    metadata,
    name,
    updated_at,
    inform_interval
) VALUES (
    now(),
    coalesce($1::jsonb, '{}'::jsonb),
    $2::text,
    coalesce($3::timestamptz, now()),
    coalesce($4::interval, '1 hour'::interval)
) ON CONFLICT (name) DO UPDATE
SET
    updated_at = coalesce($3::timestamptz, now()),
    inform_interval = coalesce($4::interval, '1 hour'::interval)
RETURNING name, last_inform_time, inform_interval, created_at, updated_at, metadata
`

type ControllerCreateOrSetUpdatedAtParams struct {
	Metadata       []byte
	Name           string
	UpdatedAt      *time.Time
	InformInterval *time.Duration
}

func (q *Queries) ControllerCreateOrSetUpdatedAt(ctx context.Context, arg *ControllerCreateOrSetUpdatedAtParams) (*DeltaController, error) {
	row := q.db.QueryRow(ctx, controllerCreateOrSetUpdatedAt,
		arg.Metadata,
		arg.Name,
		arg.UpdatedAt,
		arg.InformInterval,
	)
	var i DeltaController
	err := row.Scan(
		&i.Name,
		&i.LastInformTime,
		&i.InformInterval,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.Metadata,
	)
	return &i, err
}

const controllerGet = `-- name: ControllerGet :one
SELECT name, last_inform_time, inform_interval, created_at, updated_at, metadata FROM delta_controller
WHERE name = $1
`

func (q *Queries) ControllerGet(ctx context.Context, name string) (*DeltaController, error) {
	row := q.db.QueryRow(ctx, controllerGet, name)
	var i DeltaController
	err := row.Scan(
		&i.Name,
		&i.LastInformTime,
		&i.InformInterval,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.Metadata,
	)
	return &i, err
}

const controllerListReady = `-- name: ControllerListReady :many
SELECT name, last_inform_time, inform_interval, created_at, updated_at, metadata FROM delta_controller
WHERE
    now() - last_inform_time > inform_interval
ORDER BY last_inform_time ASC
`

func (q *Queries) ControllerListReady(ctx context.Context) ([]*DeltaController, error) {
	rows, err := q.db.Query(ctx, controllerListReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*DeltaController
	for rows.Next() {
		var i DeltaController
		if err := rows.Scan(
			&i.Name,
			&i.LastInformTime,
			&i.InformInterval,
			&i.CreatedAt,
			&i.UpdatedAt,
			&i.Metadata,
		); err != nil {
			return nil, err
		}
		items = append(items, &i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const controllerSetLastInformTime = `-- name: ControllerSetLastInformTime :exec
UPDATE delta_controller
SET 
    last_inform_time = now(),
    updated_at = now()
WHERE name = $1
`

func (q *Queries) ControllerSetLastInformTime(ctx context.Context, name string) error {
	_, err := q.db.Exec(ctx, controllerSetLastInformTime, name)
	return err
}
