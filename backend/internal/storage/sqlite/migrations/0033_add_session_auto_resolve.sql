-- +goose Up
-- auto_resolve_on_reply gates, per session, whether the SCM observer auto-resolves
-- a review thread once our side (the PR author / token user) posts a new reply on
-- it while it is still unresolved. NULL/0 = OFF (the default: resolving is left to
-- the reviewer); 1 = ON. Nullable on purpose so an untouched session is simply OFF;
-- there is no global default to inherit (unlike auto_nudge_comments).
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN auto_resolve_on_reply INTEGER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN auto_resolve_on_reply;
-- +goose StatementEnd
