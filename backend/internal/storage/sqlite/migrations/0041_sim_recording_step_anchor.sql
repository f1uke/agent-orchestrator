-- A recorded step whose label repeats used to be pinned by selector_index
-- alone. Measured against a real app, that index lands on a DIFFERENT element
-- 14% of the time and the flow still passes, because the index is counted in
-- the accessibility tree we record from and Maestro counts its own.
--
-- selector_anchor and selector_anchor_rel store the alternative: "the one
-- element with this text that lies <rel> the element labelled <anchor>", which
-- Maestro resolves entirely inside its own hierarchy and which carries no index
-- at all. Without these columns a recording could resolve an anchor at record
-- time and then lose it before the flow was written, silently falling back to
-- the index the anchor exists to replace.
--
-- Both default to empty, so every step recorded before this migration keeps
-- reading exactly as it did: no anchor, selector_rung unchanged.
--
-- A new file rather than an edit to 0038/0039: a migration already applied
-- anywhere does not re-run.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sim_recording_step ADD COLUMN selector_anchor TEXT NOT NULL DEFAULT '';
ALTER TABLE sim_recording_step ADD COLUMN selector_anchor_rel TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sim_recording_step DROP COLUMN selector_anchor;
ALTER TABLE sim_recording_step DROP COLUMN selector_anchor_rel;
-- +goose StatementEnd
