-- Summary: cards a person marked "No MR needed" on the release-train monitor.
--
-- A card that finishes without code (a config change, say) is marked here,
-- in AO only: nothing is written to Jira. The Board shows a marked card in a
-- quiet no-code state and skips it in the rules that expect an MR. HTTP
-- handlers are the only writers and nothing here is CDC: the Board's ETag
-- carries a fingerprint of these rows.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE release_card_no_mr (
    card_key  TEXT PRIMARY KEY,
    note      TEXT NOT NULL DEFAULT '',
    marked_by TEXT NOT NULL,
    marked_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE release_card_no_mr;
-- +goose StatementEnd
