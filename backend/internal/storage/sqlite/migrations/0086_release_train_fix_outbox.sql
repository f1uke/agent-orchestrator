-- Summary: the release-train fix outbox and its append-only audit log.
--
-- release_fix is one confirmed fix with the plan the person saw, including
-- every step's Before value. A hit has at most one live fix per plan
-- fingerprint, so a double confirm returns the stored fix; a withdrawn,
-- undone or superseded one does not block a new confirm. Every write to
-- another system is a dry run today: only AO's own long-lived-branch plan is
-- applied. release_fix_event is the audit log: one row per transition with
-- the actor and the request shown, never credentials, and triggers refuse any
-- UPDATE or DELETE of it. HTTP handlers and the poll pass's superseded sweep
-- are the writers; nothing here is CDC.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE release_fix (
    id               TEXT PRIMARY KEY,
    hit_id           TEXT NOT NULL,
    rule_id          TEXT NOT NULL,
    choice           TEXT NOT NULL DEFAULT '',
    plan             TEXT NOT NULL,
    plan_fingerprint TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('dry_run', 'applying', 'applied', 'failed', 'undone', 'withdrawn', 'superseded')),
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX release_fix_live ON release_fix (hit_id, plan_fingerprint)
    WHERE state IN ('dry_run', 'applying', 'applied');
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_fix_event (
    seq    INTEGER PRIMARY KEY AUTOINCREMENT,
    fix_id TEXT NOT NULL REFERENCES release_fix (id),
    at     TIMESTAMP NOT NULL,
    actor  TEXT NOT NULL,
    kind   TEXT NOT NULL CHECK (kind IN ('confirmed', 'dry_run_recorded', 'applied', 'apply_failed', 'undone', 'undo_failed', 'withdrawn', 'superseded')),
    detail TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX release_fix_event_fix ON release_fix_event (fix_id, seq);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER release_fix_event_no_update BEFORE UPDATE ON release_fix_event
BEGIN
    SELECT RAISE(ABORT, 'release_fix_event is append-only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER release_fix_event_no_delete BEFORE DELETE ON release_fix_event
BEGIN
    SELECT RAISE(ABORT, 'release_fix_event is append-only');
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER release_fix_event_no_delete;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER release_fix_event_no_update;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_fix_event;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_fix;
-- +goose StatementEnd
