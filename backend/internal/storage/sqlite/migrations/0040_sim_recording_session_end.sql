-- Two things 0038 left out, both about what happens when a recording's owning
-- session ends.
--
-- 1. A trigger that CLOSES the recording. Today an ended session leaves its
--    recording open for ever, and because udid is the primary key and
--    StartSimRecording refuses over a live one, that permanently blocks
--    `ao sim record start` for the next session on that device - a device is
--    poisoned by whoever recorded on it last. The spec (10) says session end
--    sets stopped_at and KEEPS the rows: a lease is permission and must not
--    outlive the session, a recording is evidence and should, so this stops
--    the recording rather than deleting it the way sim_lease's own trigger in
--    0035 deletes a lease. The flow can still be emitted afterwards.
--
-- 2. A foreign key on session_id, which sim_lease has had since 0035 and this
--    table never did. It makes "session_id names a real session" a fact the
--    database keeps rather than one Go remembers, and its ON DELETE CASCADE
--    covers the hard delete of a session row (a project being removed), which
--    no trigger on is_terminated ever sees. Adding a foreign key to an
--    existing SQLite table means rebuilding it, which is what the shuffle
--    below is; sim_recording_step is rebuilt with it so that dropping the old
--    parent cannot cascade the steps away mid-migration. Its new definition
--    points at sim_recording_new on purpose: ALTER TABLE ... RENAME rewrites
--    references in other tables, so after the renames it reads
--    sim_recording again.
--
-- A new file rather than an edit to 0038, for the same reason 0039 is a new
-- file: a migration already applied anywhere does not re-run, so editing one
-- in place yields a schema that silently disagrees with the code.
--
-- Column order below is 0038's plus 0039's ALTER (selector_index LAST), which
-- is where that column physically sits today. Keeping it means the queries'
-- explicit column lists still match the table's natural order and sqlc keeps
-- mapping them onto one SimRecordingStep model.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE sim_recording_new (
    udid       TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    stopped_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
-- A recording whose session row is already gone cannot satisfy the new key, so
-- it is dropped here rather than failing the migration. It could only have
-- been left by a hard session delete, which the cascade now prevents.
INSERT INTO sim_recording_new (udid, session_id, name, started_at, stopped_at, updated_at)
SELECT udid, session_id, name, started_at, stopped_at, updated_at
FROM sim_recording
WHERE session_id IN (SELECT id FROM sessions);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE sim_recording_step_new (
    udid           TEXT NOT NULL REFERENCES sim_recording_new (udid) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    at             TIMESTAMP NOT NULL,
    kind           TEXT NOT NULL,
    selector       TEXT NOT NULL DEFAULT '',
    selector_rung  INTEGER NOT NULL DEFAULT 0,
    ambiguity      INTEGER NOT NULL DEFAULT 0,
    off_screen     INTEGER NOT NULL DEFAULT 0,
    screen_change  INTEGER NOT NULL DEFAULT 0,
    x              REAL NOT NULL DEFAULT 0,
    y              REAL NOT NULL DEFAULT 0,
    to_x           REAL NOT NULL DEFAULT 0,
    to_y           REAL NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    text           TEXT NOT NULL DEFAULT '',
    detail         TEXT NOT NULL DEFAULT '',
    selector_index INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (udid, seq)
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO sim_recording_step_new (
    udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
)
SELECT
    udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
FROM sim_recording_step
WHERE udid IN (SELECT udid FROM sim_recording_new);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE sim_recording_step;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE sim_recording;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_recording_new RENAME TO sim_recording;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_recording_step_new RENAME TO sim_recording_step;
-- +goose StatementEnd
-- +goose StatementBegin
-- The trigger deletes nothing and reads by session_id, which is not the
-- primary key, so it gets the same index sim_lease's own session lookup has.
CREATE INDEX idx_sim_recording_session ON sim_recording (session_id);
-- +goose StatementEnd
-- +goose StatementBegin
-- Living next to the fact it reacts to, exactly like sim_lease's trigger, so
-- EVERY path that ends a session (stop, kill, reclaim, idle reap, PR merged,
-- issue done) closes the recording without each of them having to remember
-- to. stopped_at is taken from the session's own updated_at: that IS the
-- moment the session ended, and it is a value written by the same driver that
-- reads this column back, so no timestamp format is invented here.
CREATE TRIGGER sim_recording_stop_on_session_terminate
AFTER UPDATE OF is_terminated ON sessions
WHEN NEW.is_terminated = 1 AND OLD.is_terminated = 0
BEGIN
    UPDATE sim_recording
    SET stopped_at = NEW.updated_at, updated_at = NEW.updated_at
    WHERE session_id = NEW.id AND stopped_at IS NULL;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sim_recording_stop_on_session_terminate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sim_recording_session;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE sim_recording_old (
    udid       TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    stopped_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO sim_recording_old (udid, session_id, name, started_at, stopped_at, updated_at)
SELECT udid, session_id, name, started_at, stopped_at, updated_at FROM sim_recording;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE sim_recording_step_old (
    udid           TEXT NOT NULL REFERENCES sim_recording_old (udid) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    at             TIMESTAMP NOT NULL,
    kind           TEXT NOT NULL,
    selector       TEXT NOT NULL DEFAULT '',
    selector_rung  INTEGER NOT NULL DEFAULT 0,
    ambiguity      INTEGER NOT NULL DEFAULT 0,
    off_screen     INTEGER NOT NULL DEFAULT 0,
    screen_change  INTEGER NOT NULL DEFAULT 0,
    x              REAL NOT NULL DEFAULT 0,
    y              REAL NOT NULL DEFAULT 0,
    to_x           REAL NOT NULL DEFAULT 0,
    to_y           REAL NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    text           TEXT NOT NULL DEFAULT '',
    detail         TEXT NOT NULL DEFAULT '',
    selector_index INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (udid, seq)
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO sim_recording_step_old (
    udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
)
SELECT
    udid, seq, at, kind, selector, selector_rung, ambiguity, off_screen,
    screen_change, x, y, to_x, to_y, duration_ms, text, detail, selector_index
FROM sim_recording_step;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE sim_recording_step;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE sim_recording;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_recording_old RENAME TO sim_recording;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sim_recording_step_old RENAME TO sim_recording_step;
-- +goose StatementEnd
