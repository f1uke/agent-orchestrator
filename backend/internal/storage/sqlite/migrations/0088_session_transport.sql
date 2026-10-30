-- Summary: sessions record how their agent is driven.
--
-- transport is 'terminal' (the agent's TUI in the tmux pane, messages typed
-- into it) or 'chat' (a headless claude under `ao session-host`, messages sent
-- to its socket). It is set once at spawn and never updated, so the CDC
-- trigger does not watch it. Every row written before this column reads as
-- terminal, which is how it ran.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN transport TEXT NOT NULL DEFAULT 'terminal';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN transport;
-- +goose StatementEnd
