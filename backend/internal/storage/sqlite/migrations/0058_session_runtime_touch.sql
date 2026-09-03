-- Summary: what a task DID with a running app, kept as a fact rather than acted
-- on as a trigger.
--
-- Until now the first runtime touch CREATED the task's qa (0051). That fired at
-- the wrong end of the work: taking the simulator or pointing `ao preview` at
-- the app is the moment dev STARTS driving it, not the moment dev is finished,
-- so a qa woke up beside a dev that was still using the device and the two
-- fought over it. dev now asks for its own qa when it believes the change is
-- ready (`ao crew review`, crew_join_reason 'review').
--
-- The observation is still worth keeping, for the warning that replaces the
-- trigger: a task that drove the app and closed out with no qa ever on it is a
-- task nobody but its author looked at, and the auto-trigger existed to stop
-- exactly that from happening in silence. A task that never drove one - a
-- backend-only change - has '' here and is never nudged, which is what keeps it
-- a one-agent task that costs nothing extra.
--
-- Written once, by the first touch, on the session that did it (dev, or a solo
-- worker). Later touches are no-ops: the only question asked of this column is
-- "ever", so it needs no timestamp and no count.
--
-- '' = never drove the app, which is every row written before this existed. No
-- backfill: whether an older task drove the app is not recoverable from its row,
-- and guessing would nudge tasks that never did.
--
-- No CDC trigger change is needed. The column is read as part of the session's
-- wire object, and every write to it goes through the same UpdateSession the
-- sessions_cdc_update trigger already watches for its other columns.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN runtime_touch TEXT NOT NULL DEFAULT ''
    CHECK (runtime_touch IN ('', 'sim', 'preview'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN runtime_touch;
-- +goose StatementEnd
