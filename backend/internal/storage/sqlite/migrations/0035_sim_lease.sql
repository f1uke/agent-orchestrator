-- Summary: device leases over local iOS Simulators, so concurrent AO workers
-- cannot drive one simulator at the same time. The exclusion IS this schema:
-- udid is the primary key, so one device can carry at most one lease row, and
-- acquire is a single conditional upsert (queries/sim_lease.sql) rather than a
-- check-then-act guard in Go that a caller could forget or interleave.
--
-- Simulator HID has no touch id and no finger slot, so two overlapping gestures
-- merge into one teleporting finger and one caller's release lifts the other's
-- finger - a lost release wedges input until the device is rebooted. The unit
-- that must be exclusive is therefore a whole gesture, which is what a lease
-- (held across a caller's interaction, renewable) expresses.
--
-- Expiry is a fact about the row read at query time (expires_at <= now); there
-- is no sweeper, no poller and no background watcher.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE sim_lease (
    udid        TEXT PRIMARY KEY,             -- normalized upper-case simulator udid
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    acquired_at TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_sim_lease_session ON sim_lease (session_id);
-- +goose StatementEnd
-- +goose StatementBegin
-- A lease that outlives its owner permanently poisons a device, which is worse
-- than having no lease at all. Releasing on termination lives here, next to the
-- fact it reacts to, so EVERY path that ends a session (stop, kill, reclaim,
-- idle reap, PR merged, issue done) releases the device without each of them
-- having to remember to. It also means a restored session - is_terminated flips
-- back to 0 - cannot resurrect a lease it no longer knows it holds. A hard
-- session delete is covered by the ON DELETE CASCADE above.
CREATE TRIGGER sim_lease_release_on_session_terminate
AFTER UPDATE OF is_terminated ON sessions
WHEN NEW.is_terminated = 1 AND OLD.is_terminated = 0
BEGIN
    DELETE FROM sim_lease WHERE session_id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sim_lease_release_on_session_terminate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_lease;
-- +goose StatementEnd
