-- Summary: a machine's result on a smoke case becomes a ROW PER RUN, and every
-- evidence file names the run that captured it.
--
-- 0045 put the machine's result in four columns ON the case row
-- (agent_verdict/agent_note/agent_ran_at/agent_sha), so every `ao smoke record`
-- overwrote the previous one: exactly one machine result per case, ever, with no
-- way to look back. smoke_evidence meanwhile was keyed by check_id alone, so the
-- screenshots ACCUMULATED while the result they belonged to was destroyed.
--
-- Together that is the worst of both. A case re-run on a newer commit gave the
-- opposite verdict and both rounds' captures ended up in one undifferentiated
-- strip under the newest verdict; the earlier verdict survived only as prose in a
-- note ("the result inverted from what I recorded at d44ad432c"), because the
-- structure had nowhere to put it.
--
-- +goose Up

-- One row per `ao smoke record` round. A run OPENS when the machine attaches its
-- first artifact for the round (or at the record itself, when it captured
-- nothing) and CLOSES when the result lands, which is why recorded_at is
-- nullable: a run with evidence and no recorded_at is "the machine captured this
-- and never concluded" - a real state that used to be invisible.
--
-- verdict keeps 0045's vocabulary, including '' for "ran it, captured what I saw,
-- did not judge it". sha is the commit the round ran against; comparing it with
-- the PR head is what makes an old run readable as old rather than wrong.
-- +goose StatementBegin
CREATE TABLE smoke_run (
    id          TEXT PRIMARY KEY,
    check_id    TEXT NOT NULL REFERENCES smoke_check (id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL DEFAULT 0,   -- 1-based per case => "RUN N"
    verdict     TEXT NOT NULL DEFAULT ''
        CHECK (verdict IN ('', 'pass', 'fail', 'skip')),
    note        TEXT NOT NULL DEFAULT '',
    sha         TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMP,                    -- NULL = captured, never concluded
    created_at  TIMESTAMP NOT NULL,           -- when the round opened
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_smoke_run_check ON smoke_run (check_id, seq);
-- +goose StatementEnd

-- run_id is which run captured this file. '' is not a fallback for "unknown, take
-- the newest" - it means the row belongs to NO run, and there are exactly two
-- ways to be in that state: the user attached it (the user's lane has no runs),
-- or it is agent evidence from before run history existed. Both read correctly:
-- the first never reaches the run history, the second renders under "unknown
-- run" and says so.
--
-- The reason this column can carry that meaning is the write path: an agent
-- evidence upload OPENS a run and stamps its id, so nothing after this migration
-- ever leaves a fresh agent row at ''. No bulk adoption sweep exists to pull an
-- older image into a newer run - which is the specific lie this migration is
-- here to make impossible.
-- +goose StatementBegin
ALTER TABLE smoke_evidence ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- Backfill the ONE machine result that still exists on each case into a run. It
-- is not fabricated history: the row is what the last `ao smoke record` wrote,
-- with its own verdict, note, time and commit. Results that were overwritten
-- before this migration are gone, and no row is invented for them.
--
-- Its evidence is deliberately NOT attached. The images on such a case may come
-- from this run or from a round whose result was overwritten, and nothing in the
-- schema can tell which - so attributing them here would make a stale capture
-- read as current evidence for a verdict it may contradict. They stay '' and are
-- shown as an unknown run instead.
-- +goose StatementBegin
INSERT INTO smoke_run (id, check_id, session_id, seq, verdict, note, sha, recorded_at, created_at, updated_at)
SELECT 'run_' || id || '_1', id, session_id, 1, agent_verdict, agent_note, agent_sha,
       COALESCE(agent_ran_at, updated_at), COALESCE(agent_ran_at, updated_at), updated_at
FROM smoke_check
WHERE agent_ran_at IS NOT NULL OR agent_verdict <> '';
-- +goose StatementEnd

-- The four columns go. Keeping them beside the run rows would be a second source
-- of truth for the same fact, and the two would drift the first time anything
-- wrote one without the other. The API keeps the same four fields, derived from
-- the latest RECORDED run, so nothing downstream has to change to read them.
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_verdict;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_note;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_ran_at;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check DROP COLUMN agent_sha;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_verdict TEXT NOT NULL DEFAULT ''
    CHECK (agent_verdict IN ('', 'pass', 'fail', 'skip'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_ran_at TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_check ADD COLUMN agent_sha TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- Going back keeps only what the old shape can hold: the latest recorded run.
-- The earlier runs are dropped with the table, which is the same loss the old
-- shape imposed every time it was written.
-- +goose StatementBegin
UPDATE smoke_check SET
    agent_verdict = COALESCE((SELECT r.verdict FROM smoke_run r
        WHERE r.check_id = smoke_check.id AND r.recorded_at IS NOT NULL
        ORDER BY r.seq DESC LIMIT 1), ''),
    agent_note = COALESCE((SELECT r.note FROM smoke_run r
        WHERE r.check_id = smoke_check.id AND r.recorded_at IS NOT NULL
        ORDER BY r.seq DESC LIMIT 1), ''),
    agent_sha = COALESCE((SELECT r.sha FROM smoke_run r
        WHERE r.check_id = smoke_check.id AND r.recorded_at IS NOT NULL
        ORDER BY r.seq DESC LIMIT 1), ''),
    agent_ran_at = (SELECT r.recorded_at FROM smoke_run r
        WHERE r.check_id = smoke_check.id AND r.recorded_at IS NOT NULL
        ORDER BY r.seq DESC LIMIT 1);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE smoke_evidence DROP COLUMN run_id;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS smoke_run;
-- +goose StatementEnd
