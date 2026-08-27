-- Summary: both crew members author the task's checklist, so every case records
-- WHO last changed it, and a checklist can say "I looked, there is nothing a
-- human needs to check".
--
-- Attribution exists because the checklist stopped belonging to one member. The
-- human's stated reason for wanting it: they want to see which cases came from
-- dev, who knows the call sites, and which from qa, who sees it as a user would.
-- It is NOT a safety mechanism - it records who changed a case, it cannot stop
-- anyone destroying one. What makes two authors safe is the per-case write path
-- (add/edit/remove one case) that landed with this migration; attribution is the
-- part that makes a shared list readable afterwards.
--
-- Every column defaults, so every existing row stays valid without a backfill:
-- '' / NULL reads as "authored before AO recorded who", which is true.
-- +goose Up

-- authored_by is the session id of the last member to write this case's
-- AUTHORED fields (name/why/steps/expected/prNum/fileRef). Deliberately not
-- touched by a verdict, a machine result or a retire: "who wrote this case" and
-- "who last touched this row" are different questions, and updated_at already
-- answers the second.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN authored_by TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- authored_by_role is a SNAPSHOT of the writer's crew role, not a join. Taken at
-- write time it records what was true when the change was made, needs no
-- read-time session lookup, and survives the session row being reaped. '' covers
-- a solo worker and every caller AO cannot identify (the desktop app, a direct
-- API call, an older `ao`) - an unidentified author renders as no author, never
-- as a guess.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN authored_by_role TEXT NOT NULL DEFAULT ''
    CHECK (authored_by_role IN ('', 'dev', 'qa'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN authored_at TIMESTAMP;
-- +goose StatementEnd

-- smoke_checklist_state holds the one fact that belongs to the CHECKLIST rather
-- than to any case: that a member looked and concluded nothing here needs a
-- person's eyes. Without it an empty Tests tab says two different things at once
-- - nobody has decided yet, or it was decided and there is nothing worth your
-- eyes - and the screen renders them identically. One row per session, created
-- on stand-down and deleted the moment a case is authored (a case on the list
-- contradicts the claim, so the claim goes).
-- +goose StatementBegin
CREATE TABLE smoke_checklist_state (
    session_id          TEXT PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    stood_down_at       TIMESTAMP NOT NULL,
    stood_down_by       TEXT NOT NULL DEFAULT '',
    stood_down_by_role  TEXT NOT NULL DEFAULT ''
        CHECK (stood_down_by_role IN ('', 'dev', 'qa')),
    reason              TEXT NOT NULL,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS smoke_checklist_state;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN authored_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN authored_by_role;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN authored_by;
-- +goose StatementEnd
