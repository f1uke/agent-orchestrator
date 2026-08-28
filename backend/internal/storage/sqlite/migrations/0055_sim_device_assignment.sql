-- Summary: which simulator belongs to which session. A device ASSIGNMENT is
-- not a lease and does not replace one: the lease says who may drive a device
-- right now, and it already refuses to share. The hole this closes is that
-- nothing told an agent which device was supposed to be ITS OWN, so with one
-- device booted every session reached for that one - including the crewmate's,
-- mid-verification.
--
-- The assignment therefore has no TTL. It is held for the life of the session
-- and exported into its environment (AO_SIM_UDID / AO_SIM_DESTINATION) at
-- spawn, so `ao sim` and a raw `xcodebuild -destination` land on the same
-- device without the agent having to remember anything - which is the only
-- kind of fix that survives an agent forgetting.
--
-- Exclusion is the schema, exactly as it is for sim_lease: session_id is the
-- primary key so a session holds at most one device, and udid is UNIQUE so a
-- device is assigned to at most one session. Two racing spawns resolve to one
-- winner in the database; the loser picks again.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE sim_device_assignment (
    session_id  TEXT PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    udid        TEXT NOT NULL UNIQUE,       -- normalized upper-case simulator udid
    assigned_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
-- Same reasoning as sim_lease_release_on_session_terminate: an assignment that
-- outlives its owner permanently removes a device from the pool, and putting
-- the release next to the fact it reacts to means EVERY path that ends a
-- session gives the device back without having to remember to. A restored
-- session is re-assigned on its next spawn, which is also what re-exports the
-- environment variable it needs.
CREATE TRIGGER sim_device_assignment_release_on_session_terminate
AFTER UPDATE OF is_terminated ON sessions
WHEN NEW.is_terminated = 1 AND OLD.is_terminated = 0
BEGIN
    DELETE FROM sim_device_assignment WHERE session_id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sim_device_assignment_release_on_session_terminate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sim_device_assignment;
-- +goose StatementEnd
