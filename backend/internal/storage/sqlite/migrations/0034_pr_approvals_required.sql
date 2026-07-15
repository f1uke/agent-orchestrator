-- approvals_required is the approval count the SCM's OWN rule requires, when it
-- enforces one (GitLab: approvals_required). 0 when the SCM exposes no numeric
-- threshold (GitHub) or enforces no rule of its own. Paired with the existing
-- approval_rule_configured flag, it lets the display surfaces show A/T progress
-- for an SCM-native rule; AO's per-project rule uses its own threshold instead.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE pr ADD COLUMN approvals_required INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pr DROP COLUMN approvals_required;
-- +goose StatementEnd
