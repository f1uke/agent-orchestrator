-- A recorded step's selector can match more than one element on screen
-- (selector_rung = RungTextIndex); selector_index is which of those matches,
-- in tree order, the step actually resolved to. Without it, re-emitting a
-- flow from a stored recording always picks index 0 - the FIRST element with
-- that label - even when the human tapped the second or third one, and the
-- emitted flow's selector text would still read as correct while touching
-- the wrong element.
--
-- A new file rather than an edit to 0038: a migration already applied
-- anywhere does not re-run, so editing 0038 in place would leave a database
-- that applied it before this column existed silently out of step with the
-- code that now expects it.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sim_recording_step ADD COLUMN selector_index INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sim_recording_step DROP COLUMN selector_index;
-- +goose StatementEnd
