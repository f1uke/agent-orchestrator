-- +goose Up
-- task_size records the ceremony level the orchestrator picked for a worker at
-- spawn (`ao spawn --task-size`): mechanical / standard / deep. It drives ONLY
-- the worker system prompt: a mechanical task is authorized to skip the
-- heavyweight process skills (brainstorming / writing-plans / TDD) and go straight
-- to edit + verify, cutting the turn-count blow-up a small change would otherwise
-- incur (feat/right-size-worker-cost). Persisted so a restore or a TODO->Start
-- rebuilds the prompt at the right level. Default 'standard' (full ceremony), so
-- existing rows stay valid without backfill and an omitted flag means no skip.
-- Set once at create and never toggled, so (unlike keep_warm_on_merge) it needs
-- no sessions_cdc_update trigger change (no client watches it).
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN task_size TEXT NOT NULL DEFAULT 'standard';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN task_size;
-- +goose StatementEnd
