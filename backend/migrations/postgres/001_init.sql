-- 001_init: базовая схема (postgres). Отличия от sqlite: JSONB для payload,
-- BIGINT вместо INTEGER, TIMESTAMPTZ не используется — время храним как unix ms.
CREATE TABLE IF NOT EXISTS events (
    event_id       TEXT PRIMARY KEY,
    node_id        TEXT NOT NULL,
    sequence       BIGINT NOT NULL,
    event_type     TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    aggregate_id   TEXT NOT NULL,
    occurred_at    BIGINT NOT NULL, -- unix ms
    payload        JSONB NOT NULL,
    payload_hash   TEXT NOT NULL,
    model_version  TEXT NOT NULL DEFAULT '',
    UNIQUE (node_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_events_event_type   ON events (event_type);
CREATE INDEX IF NOT EXISTS idx_events_aggregate_id ON events (aggregate_id);
CREATE INDEX IF NOT EXISTS idx_events_occurred_at  ON events (occurred_at);

CREATE TABLE IF NOT EXISTS horses (
    id              TEXT PRIMARY KEY,
    created_at      BIGINT NOT NULL,
    identity_status TEXT NOT NULL DEFAULT 'active',
    metadata        JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS observations (
    id                TEXT PRIMARY KEY,
    horse_id          TEXT NOT NULL DEFAULT '',
    node_id           TEXT NOT NULL,
    observed_at       BIGINT NOT NULL,
    location          TEXT NOT NULL DEFAULT '',
    observer_id       TEXT NOT NULL,
    blob_ids          JSONB NOT NULL DEFAULT '[]',
    trait_snapshot_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_observations_horse_id    ON observations (horse_id);
CREATE INDEX IF NOT EXISTS idx_observations_observed_at ON observations (observed_at);

CREATE TABLE IF NOT EXISTS trait_snapshots (
    id             TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL,
    model_version  TEXT NOT NULL DEFAULT '',
    traits         JSONB NOT NULL DEFAULT 'null',
    confidence     DOUBLE PRECISION NOT NULL DEFAULT 0,
    fingerprint    TEXT NOT NULL DEFAULT '',
    confirmed      BOOLEAN NOT NULL DEFAULT FALSE,
    edited         BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_trait_snapshots_observation_id ON trait_snapshots (observation_id);

CREATE TABLE IF NOT EXISTS blobs (
    id        TEXT PRIMARY KEY,
    sha256    TEXT NOT NULL,
    mime_type TEXT NOT NULL DEFAULT '',
    size      BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    UNIQUE (sha256)
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at BIGINT NOT NULL
);
