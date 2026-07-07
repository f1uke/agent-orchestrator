-- +goose Up
-- reactivated marks a session that was brought back from a terminal state via
-- `ao session restore` (the board Reopen action). Status derivation surfaces a
-- reactivated, non-terminated session with no open PR as needs_input (the
-- "Needs you" zone), so a reopened session lands back on the board instead of
-- staying pinned to Done by a previously-merged PR — until it takes on new work
-- or is finished again. It only ever flips alongside is_terminated (restore
-- clears terminated and sets reactivated in the same write), so the existing
-- sessions_cdc_update trigger already fans out the change; no trigger change is
-- needed here.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN reactivated BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN reactivated;
-- +goose StatementEnd
