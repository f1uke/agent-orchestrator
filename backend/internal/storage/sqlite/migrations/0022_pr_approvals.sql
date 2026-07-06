-- Approval facts for the min-approvals threshold. approvals_count is the number
-- of distinct approvers reported by the SCM; approval_rule_configured is 1 when
-- the SCM enforces an approval rule of its own (GitLab: has_approval_rules or
-- approvals_required > 0). AO's per-project minApprovals floor applies only when
-- approval_rule_configured = 0. Populated by the GitLab adapter; other providers
-- leave the defaults.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE pr ADD COLUMN approvals_count INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE pr ADD COLUMN approval_rule_configured INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pr DROP COLUMN approval_rule_configured;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE pr DROP COLUMN approvals_count;
-- +goose StatementEnd
