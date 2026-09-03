-- Add 'review' to the sessions.crew_join_reason vocabulary.
--
-- 0051's three values were written for a qa AO created by OBSERVING dev: 'sim'
-- and 'preview' were the two observations, and 'manual' was the escape hatch a
-- person used. The observations are retired (see 0058) because they fire when
-- dev STARTS driving the app rather than when it is done, so the ordinary way a
-- task gains a qa is now dev asking for one - and that is neither an observation
-- nor a person, so it needs its own value.
--
-- It has to be distinguishable from 'manual' rather than folded into it. The
-- reason is the only durable record of what created a member, and both the
-- board's join line and the joining member's own first turn are written from it:
-- 'manual' opens qa's kickoff with "A HUMAN added you to this task", which for a
-- dev-requested qa is false and points it at the wrong first question.
--
-- No backfill: no row has ever been written with this value, and no earlier row
-- can be reclassified into it. The two retired values keep their meaning for the
-- rows that carry them.
--
-- SQLite cannot ALTER a CHECK, so this follows 0043/0057's pattern and surgically
-- rewrites the stored CREATE TABLE text in sqlite_master. writable_schema edits
-- must run outside a transaction, and RESET forces an immediate schema reparse
-- on the connection.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (crew_join_reason IN ('''', ''sim'', ''preview'', ''manual''))',
    'CHECK (crew_join_reason IN ('''', ''sim'', ''preview'', ''manual'', ''review''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- A down-migration must not leave rows the CHECK rejects. A dev-requested qa
-- folds onto 'manual', the closest surviving value: somebody asked for it rather
-- than AO observing, which is the whole of what 'manual' says.
-- +goose StatementBegin
UPDATE sessions SET crew_join_reason = 'manual' WHERE crew_join_reason = 'review';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (crew_join_reason IN ('''', ''sim'', ''preview'', ''manual'', ''review''))',
    'CHECK (crew_join_reason IN ('''', ''sim'', ''preview'', ''manual''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
