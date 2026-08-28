-- Summary: when the user confirms a verdict the machine already reached, the row
-- records WHICH machine run they agreed with - and records it in the USER's lane.
--
-- The Tests tab now offers "Agree - record Pass" beside qa's result, because
-- re-deriving a conclusion you already believe and then hunting for the matching
-- button is the slow path through a checklist. The button must not become a way
-- for the machine to fill the human's lane: AO protects the user's verdict as the
-- one thing it cannot regenerate, and "0 of 7 verified" only means "a person
-- looked" for as long as nothing but a person can move it.
--
-- So agreement is stored as a fact ABOUT the user's verdict, not as a second
-- author of it. verdict/note/decided_at are written exactly as a hand-pressed
-- Pass writes them; agreed_run_id is the extra sentence "and I got there by
-- agreeing with that run". No smoke_run row is created, and nothing in the
-- machine's lane is touched.
--
-- It names a RUN and not merely "qa", because since 0053 a case can have failed
-- at one commit and passed at another. "Agreed with qa" would be ambiguous the
-- moment two runs disagree; "agreed with run 3 (a1b2c3d)" cannot be.
-- +goose Up

-- '' means "the user reached this verdict themselves", which is what every row
-- before this migration did - so the default needs no backfill and invents no
-- agreement that never happened. Deliberately NOT a foreign key to smoke_run: a
-- run row is the machine's, and no cascade from the machine's lane may ever
-- reach the user's verdict. If the named run somehow vanished, the verdict is
-- still the user's - it just reads as a hand-made call again.
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agreed_run_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agreed_run_id;
-- +goose StatementEnd
