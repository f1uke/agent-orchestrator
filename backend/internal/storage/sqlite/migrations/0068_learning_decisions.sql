-- Summary: the person's decisions on learning proposals (slice 5 inbox).
--
-- A pending proposal can be approved (its change is written and the hash of
-- what was written kept), rejected with a reason (decide reads it so the same
-- thing is not proposed again), or snoozed until a time (it stays pending and
-- comes back then). A conflict card records which side won. A proposal whose
-- target changed after it was made goes stale instead of overwriting.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN snoozed_until TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN reject_reason TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN decided_at TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN applied_sha256 TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal ADD COLUMN resolution TEXT NOT NULL DEFAULT ''
    CHECK (resolution IN ('', 'keep_rule', 'words_win', 'both'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN resolution;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN applied_sha256;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN decided_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN reject_reason;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE learn_proposal DROP COLUMN snoozed_until;
-- +goose StatementEnd
