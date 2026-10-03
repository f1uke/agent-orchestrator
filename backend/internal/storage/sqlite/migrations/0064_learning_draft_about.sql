-- Summary: what a candidate lesson is about, as the collect model tagged it.
--
-- The human labelled 60 drafts: the false lessons were mostly decisions about
-- the product being built and one-off directions. Telling the model to leave
-- them out also cost about 30% of the real lessons; asking it to TAG them kept
-- the lessons and separated them (a filter on agent_practice reached 94%
-- precision). So collect keeps every lesson with its tag, and the decide stage
-- weighs it. Drafts collected before this have '' and are judged untagged.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE learn_draft ADD COLUMN about TEXT NOT NULL DEFAULT ''
    CHECK (about IN ('', 'agent_practice', 'product_decision', 'one_off', 'question'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE learn_draft DROP COLUMN about;
-- +goose StatementEnd
