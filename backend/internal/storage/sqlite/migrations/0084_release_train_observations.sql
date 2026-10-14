-- Summary: the release-train monitor's observations, one generic row per
-- polled resource and one per fact item.
--
-- release_resource is one resource a source fetches (a project's MRs, one
-- Jira version's cards) with its own freshness, cursor and backoff, so a
-- restart honours a 429 window. release_item holds the core fact as JSON,
-- not the wire payload, so a new source or fact field needs no migration.
-- planned_at is when a source last planned the resource; rows unplanned for
-- 24h are deleted with their items. The poller is the only writer and nothing
-- here is CDC: the page refetches the board on an ETag instead.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE release_resource (
    kind             TEXT NOT NULL,
    scope            TEXT NOT NULL,
    app_id           TEXT,
    sync_mode        TEXT NOT NULL CHECK (sync_mode IN ('snapshot', 'incremental')),
    fact_version     INTEGER NOT NULL,
    cursor           TEXT,
    complete_at      TIMESTAMP,
    last_attempt_at  TIMESTAMP,
    last_success_at  TIMESTAMP,
    failures         INTEGER NOT NULL DEFAULT 0,
    error_class      TEXT CHECK (error_class IN ('auth', 'rate_limited', 'unavailable', 'not_found', 'decode', 'truncated', 'unconfigured')),
    error_message    TEXT,
    next_attempt_at  TIMESTAMP,
    planned_at       TIMESTAMP NOT NULL,
    generation       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (kind, scope)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_item (
    kind              TEXT NOT NULL,
    scope             TEXT NOT NULL,
    item_key          TEXT NOT NULL,
    body              TEXT NOT NULL,
    source_updated_at TIMESTAMP,
    PRIMARY KEY (kind, scope, item_key),
    FOREIGN KEY (kind, scope) REFERENCES release_resource (kind, scope) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE release_item;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_resource;
-- +goose StatementEnd
