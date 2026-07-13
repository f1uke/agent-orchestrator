-- +goose Up
-- Per-session token telemetry (R2 from the token-usage investigation). AO records
-- ZERO token usage today: it launches the Claude Code harness and never reads back
-- the real per-message usage the harness writes to ~/.claude/projects/**.jsonl, so a
-- runaway session is invisible in-app. These columns hold the four raw token buckets
-- summed over the transcript's assistant messages plus the turn count they came
-- from. They are DURABLE MEASURED FACTS only: the raw total, the cost-weighted total
-- (Anthropic cache multipliers), and the runaway flag are all DERIVED at read time,
-- so the weighting formula / threshold can change without a migration or backfill.
--
-- Written exclusively by the dedicated SetSessionTokenUsage updater (the token-usage
-- observer), never by the full-row InsertSession/UpdateSession path, so a concurrent
-- lifecycle write can never clobber a fresh parse (no read-modify-write race).
--
-- No sessions_cdc_update trigger change on purpose: a live session's totals refresh
-- on every poll, and fanning that into change_log would spam CDC on every turn. The
-- board picks the numbers up on its normal periodic session refetch instead.
--
-- tokens_updated_at is nullable with no default: NULL means "never parsed" (no
-- telemetry available yet — a fresh session, or a non-claude-code agent whose
-- transcript AO cannot read), which the wire layer maps to "no chip / n/a". The
-- integer buckets default to 0 so existing rows stay valid without backfill.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN token_input INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN token_cache_creation INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN token_cache_read INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN token_output INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN token_turns INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN tokens_updated_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN tokens_updated_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN token_turns;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN token_output;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN token_cache_read;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN token_cache_creation;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN token_input;
-- +goose StatementEnd
