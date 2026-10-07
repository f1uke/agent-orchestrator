-- Summary: a session's simulators are now clones AO makes for it from a few
-- base devices, and AO deletes them when the session ends. sim_clone replaces
-- sim_device_assignment, which handed out whatever device was free - with no
-- notion of model and no notion of who needed one, so orchestrators held
-- iPhones and an iOS worker was given an iPad.
--
-- A row is the proof that AO made the device. Nothing deletes a device without
-- one, so a human's simulator, or one another AO daemon made, is never touched.
--
-- session_id is deliberately NOT a foreign key and nothing deletes a row when
-- its session ends: the row is what tells the sweep which device to delete, so
-- it has to outlive the session. The sweep deletes the device first and the row
-- after it.
--
-- The old assignments are dropped, not converted: they point at devices AO did
-- not make (the bases among them), and AO must never delete those. A live
-- session that held one is given a clone the next time it is spawned or
-- restored.
-- +goose Up
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sim_device_assignment_release_on_session_terminate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_device_assignment;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE sim_clone (
    udid       TEXT      PRIMARY KEY CHECK (udid <> ''),   -- normalized upper-case simulator udid
    session_id TEXT      NOT NULL CHECK (session_id <> ''),
    label      TEXT      NOT NULL CHECK (label <> ''),     -- 'primary' is the AO_SIM_UDID device
    base       TEXT      NOT NULL CHECK (base <> ''),      -- name of the base device it was cloned from
    name       TEXT      NOT NULL CHECK (name <> ''),      -- the clone's own simctl name
    created_at TIMESTAMP NOT NULL,
    UNIQUE (session_id, label)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_clone;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE sim_device_assignment (
    session_id  TEXT PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    udid        TEXT NOT NULL UNIQUE,
    assigned_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sim_device_assignment_release_on_session_terminate
AFTER UPDATE OF is_terminated ON sessions
WHEN NEW.is_terminated = 1 AND OLD.is_terminated = 0
BEGIN
    DELETE FROM sim_device_assignment WHERE session_id = NEW.id;
END;
-- +goose StatementEnd
