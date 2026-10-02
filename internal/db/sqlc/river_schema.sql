-- Minimal subset of River's river_job table so sqlc can type-check delta
-- queries that reference it. This file is NOT a migration: River owns and
-- migrates the real table (see rivermigrate).
CREATE TABLE river_job(
    id bigserial PRIMARY KEY,
    state text NOT NULL,
    queue text NOT NULL,
    kind text NOT NULL,
    args jsonb NOT NULL,
    scheduled_at timestamptz NOT NULL
);
