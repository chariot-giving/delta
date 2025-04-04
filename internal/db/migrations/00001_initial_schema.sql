-- +goose Up
CREATE TYPE delta_resource_state AS ENUM(
    'synced',
    'pending',
    'expired',
    'scheduled',
    'failed',
    'degraded',
    'deleted',
    'unknown'
);

CREATE TABLE delta_namespace(
    name text PRIMARY KEY NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    metadata jsonb NOT NULL DEFAULT '{}' ::jsonb,
    updated_at timestamptz NOT NULL,
    expiry_ttl integer NOT NULL DEFAULT 0
);

CREATE TABLE delta_resource(
    -- 8 bytes
    id bigserial PRIMARY KEY,

    -- 8 bytes (4 bytes + 2 bytes + 2 bytes)
    --
    -- `state` is kept near the top of the table for operator convenience -- when
    -- looking at jobs with `SELECT *` it'll appear first after ID. The other two
    -- fields aren't as important but are kept adjacent to `state` for alignment
    -- to get an 8-byte block.
    state delta_resource_state NOT NULL DEFAULT 'unknown',
    attempt smallint NOT NULL DEFAULT 0,
    max_attempts smallint NOT NULL,

    -- 8 bytes each (no alignment needed)
    attempted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    synced_at timestamptz,
    external_created_at timestamptz,

    -- types stored out-of-band
    object_id text NOT NULL,
    kind text NOT NULL,
    namespace text NOT NULL DEFAULT 'default',
    object jsonb NOT NULL DEFAULT '{}',
    hash bytea,
    metadata jsonb NOT NULL DEFAULT '{}',
    tags varchar(255)[] NOT NULL DEFAULT '{}',
    errors jsonb[],

    CONSTRAINT synced_or_synced_at_null CHECK (
        (synced_at IS NULL AND state NOT IN ('synced', 'expired', 'deleted')) OR
        (synced_at IS NOT NULL AND state IN ('synced', 'expired', 'deleted'))
    ),
    CONSTRAINT max_attempts_is_positive CHECK (max_attempts > 0),
    CONSTRAINT namespace_length CHECK (char_length(namespace) > 0 AND char_length(namespace) < 128),
    CONSTRAINT kind_length CHECK (char_length(kind) > 0 AND char_length(kind) < 128),
    CONSTRAINT object_id_length CHECK (char_length(object_id) > 0 AND char_length(object_id) < 255)
);

CREATE UNIQUE INDEX delta_resource_object_id_kind_unique ON delta_resource(object_id, kind);
CREATE INDEX delta_resource_kind ON delta_resource USING btree(kind);
CREATE INDEX delta_resource_object_id ON delta_resource USING btree(object_id);
CREATE INDEX delta_resource_external_created_at ON delta_resource USING btree(external_created_at);

-- use Generalized Inverted Index
CREATE INDEX delta_resource_object_index ON delta_resource USING GIN(object);
CREATE INDEX delta_resource_metadata_index ON delta_resource USING GIN(metadata);

CREATE TABLE delta_controller (
    name text PRIMARY KEY NOT NULL,
    last_inform_time TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00Z',
    inform_interval interval NOT NULL DEFAULT '1 hour',
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    CONSTRAINT name_length CHECK (char_length(name) > 0 AND char_length(name) < 128)
);

-- +goose Down
DROP TABLE delta_controller;
DROP TABLE delta_resource;
DROP TABLE delta_namespace;
DROP TYPE delta_resource_state;
