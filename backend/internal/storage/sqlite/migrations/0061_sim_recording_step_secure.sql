-- A typed step that went into a secure field. The recorder no longer keeps
-- what was typed there - text stays empty - and the flow emits the paste the
-- scripts require (long-press the field, the system Paste, assert the dots)
-- instead of an inputText that would carry the password. Without this column
-- a stop could not tell that empty text from a step that typed nothing.
--
-- Defaults to 0, so every step recorded before this migration reads exactly
-- as it did.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sim_recording_step ADD COLUMN secure INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sim_recording_step DROP COLUMN secure;
-- +goose StatementEnd
