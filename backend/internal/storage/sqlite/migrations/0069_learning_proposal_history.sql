-- Summary: a decided or snoozed learning proposal stays actionable.
--
-- A snoozed proposal can be unsnoozed, a rejected one reopened, and an applied
-- one edited or undone (its memory file and MEMORY.md line taken back, or the
-- file it changed restored). Undo needs what the write replaced and the index
-- line it added, so both are kept with the proposal. Every decision is also
-- kept as an event - what was decided, when, and from which surface - instead
-- of only the latest status.
--
-- Rows decided before this migration get one backfilled event each. An applied
-- new memory gets its index line as the line it added (apply adds it unless the
-- index already had it); a changed file applied before this keeps an empty
-- applied_before, and undo then reads the backup apply made at decided_at.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN applied_before TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN applied_index_line TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE learn_proposal SET applied_index_line = index_line
WHERE status = 'applied' AND action = 'create_memory';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE learn_proposal_event (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    proposal_id   INTEGER NOT NULL REFERENCES learn_proposal (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL
        CHECK (kind IN ('approved', 'edited', 'rejected', 'snoozed', 'unsnoozed', 'reopened', 'undone', 'stale')),
    -- The proposal's status after the event.
    status        TEXT NOT NULL,
    -- The reason of a rejection, the side of a conflict, what an undo did.
    note          TEXT NOT NULL DEFAULT '',
    snoozed_until TIMESTAMP,
    -- Which surface decided: the app, the ao CLI (with the AO session that ran
    -- it, if any), or a bare API call. '' is a decision made before this was kept.
    via           TEXT NOT NULL DEFAULT '' CHECK (via IN ('', 'app', 'cli', 'api')),
    session_id    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX learn_proposal_event_by_proposal ON learn_proposal_event (proposal_id, id);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO learn_proposal_event (proposal_id, kind, status, note, created_at)
SELECT id,
    CASE status WHEN 'applied' THEN 'approved' WHEN 'rejected' THEN 'rejected' ELSE 'stale' END,
    status,
    CASE WHEN status = 'rejected' THEN reject_reason ELSE resolution END,
    decided_at
FROM learn_proposal
WHERE decided_at IS NOT NULL AND status IN ('applied', 'rejected', 'stale')
ORDER BY id;
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO learn_proposal_event (proposal_id, kind, status, snoozed_until, created_at)
SELECT id, 'snoozed', status, snoozed_until, updated_at
FROM learn_proposal
WHERE status = 'pending' AND snoozed_until IS NOT NULL
ORDER BY id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE learn_proposal_event;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN applied_index_line;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN applied_before;
-- +goose StatementEnd
