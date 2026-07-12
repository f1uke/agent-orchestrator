-- +goose Up
-- last_opened_at records when the user last OPENED/selected this session (the
-- /wake signal fired from SessionView on open/select). It feeds ONLY the
-- idle-suspend keepalive (idleReference = the later of activity_last_at and
-- last_opened_at), never status derivation — so merely viewing a session
-- refreshes its 72h idle-suspend TTL WITHOUT bumping activity_last_at, which the
-- needs-input aging / working-status derivation reads. Before this column,
-- opening a session touched activity_last_at, re-aging an already-"Needs you"
-- session back to idle/"Recently active" with a fresh countdown (the bug this
-- fixes). Nullable with no default, so existing rows stay valid with NULL (=
-- never opened → idleReference falls back to activity_last_at / created_at). No
-- CDC trigger change: a wake changes no CDC-watched column, so it stays quiet
-- and the opening client refetches via its own query invalidation.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN last_opened_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN last_opened_at;
-- +goose StatementEnd
