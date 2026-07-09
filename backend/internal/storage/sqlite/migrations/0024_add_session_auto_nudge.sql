-- +goose Up
-- auto_nudge_comments is a per-session override for the "auto-nudge the worker
-- when a PR has unresolved review comments" behavior. NULL = inherit the global
-- default (autonudge settings store); 0 = force off; 1 = force on. Nullable on
-- purpose: an untouched session has no opinion and follows the global default.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN auto_nudge_comments INTEGER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN auto_nudge_comments;
-- +goose StatementEnd
