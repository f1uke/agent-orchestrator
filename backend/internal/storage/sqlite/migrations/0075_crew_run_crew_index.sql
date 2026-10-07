-- Summary: index crew_run by crew so a task's Machine runs (every member's runs,
-- matched by session_id OR crew_id) reads two indexes instead of the table.
-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_crew_run_crew ON crew_run (crew_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_crew_run_crew;
-- +goose StatementEnd
