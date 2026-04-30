-- +goose Up
ALTER TABLE delta_controller
    ADD COLUMN reconcile_interval interval NOT NULL DEFAULT '0',
    ADD COLUMN last_reconcile_time timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00Z';

-- +goose Down
ALTER TABLE delta_controller
    DROP COLUMN last_reconcile_time,
    DROP COLUMN reconcile_interval;
