-- Summary: what people set on the release-train monitor: rule toggles,
-- hidden hits and long-lived-branch plans.
--
-- release_rule_setting overrides a rule's default on/off; no row means the
-- default. release_hit_hide keys a hide by the hit's stable id and keeps the
-- evidence fingerprint and severity at hide time, so the Board can say
-- "changed since hidden" and bring a hit back when its severity rises. A
-- long-lived-branch plan has a version for optimistic writes; a branch is in
-- at most one plan. HTTP handlers are the only writers and nothing here is
-- CDC: the Board's ETag carries a fingerprint of these rows.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE release_rule_setting (
    rule_id    TEXT PRIMARY KEY,
    enabled    BOOLEAN NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_hit_hide (
    hit_id    TEXT PRIMARY KEY,
    evidence  TEXT NOT NULL,
    severity  TEXT NOT NULL CHECK (severity IN ('block', 'data', 'risk', 'hygiene', 'info')),
    reason    TEXT NOT NULL DEFAULT '',
    hidden_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_llb_plan (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    target_kind  TEXT NOT NULL CHECK (target_kind IN ('train', 'unplanned', 'dropped')),
    target_train TEXT,
    note         TEXT NOT NULL DEFAULT '',
    version      INTEGER NOT NULL,
    updated_by   TEXT NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    CHECK ((target_kind = 'train') = (target_train IS NOT NULL))
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_llb_plan_branch (
    plan_id TEXT NOT NULL REFERENCES release_llb_plan (id) ON DELETE CASCADE,
    app_id  TEXT NOT NULL,
    branch  TEXT NOT NULL,
    PRIMARY KEY (app_id, branch)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE release_llb_plan_accepted (
    plan_id TEXT NOT NULL REFERENCES release_llb_plan (id) ON DELETE CASCADE,
    mr_ref  TEXT NOT NULL,
    reason  TEXT NOT NULL,
    PRIMARY KEY (plan_id, mr_ref)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE release_llb_plan_accepted;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_llb_plan_branch;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_llb_plan;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_hit_hide;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE release_rule_setting;
-- +goose StatementEnd
