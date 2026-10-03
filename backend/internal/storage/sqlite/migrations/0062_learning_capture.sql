-- Summary: learning capture - the bookkeeping that lets AO keep what the human
-- taught its agents after Claude Code prunes the transcript it was said in.
--
-- The human teaches workers directly in the pane: corrections, rules, the steps
-- of a flow, the reason behind a decision. None of that survives into a final
-- report, and Claude Code deletes a transcript 30 days after it was last written.
-- These tables turn an opted-in project's transcripts into REDACTED excerpts of
-- the human's turns, each with a bounded window of what the agent did around it,
-- so a later stage can propose skills from them without ever reading a transcript
-- again. Nothing here calls a model.
--
-- Every table is keyed to a project and cascades with it: deleting a project
-- deletes what was learned from it. Nothing is written for a project whose
-- config has learnFromSessions off; that gate is enforced in the daemon, before
-- any of these tables is touched.
--
-- No CDC triggers: nothing on screen watches these rows yet.
-- +goose Up

-- Which transcript files belong to which AO session. Claude Code is launched
-- with a pinned --session-id, but `/clear` starts a NEW conversation file under a
-- new id, and AO used to have no way to learn its name. Every Claude Code hook
-- envelope carries session_id and transcript_path; `ao hooks` reports them here.
-- A path is not content, so this table is written for every project.
-- +goose StatementBegin
CREATE TABLE session_transcript (
    session_id        TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    transcript_path   TEXT NOT NULL,
    claude_session_id TEXT NOT NULL DEFAULT '',
    first_seen_at     TIMESTAMP NOT NULL,
    last_seen_at      TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, transcript_path)
);
-- +goose StatementEnd

-- One row per prompt the agent's UserPromptSubmit hook saw: a sha256 of the
-- normalized text and its length, never the text. It is the canary that tells
-- AO whether its transcript parser still finds every submitted prompt - Claude
-- Code's transcript format is undocumented and can change under us. Capture
-- stamps matched_at when it reads a typed turn with the same fingerprint; a
-- prompt that stays unmatched long after capture ran is a turn the parser
-- missed.
-- +goose StatementBegin
CREATE TABLE prompt_fingerprint (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id        TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    claude_session_id TEXT NOT NULL DEFAULT '',
    sha256            TEXT NOT NULL,
    bytes             INTEGER NOT NULL,
    submitted_at      TIMESTAMP NOT NULL,
    matched_at        TIMESTAMP
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_prompt_fingerprint_session ON prompt_fingerprint (session_id, submitted_at);
-- +goose StatementEnd

-- One row per message AO itself put into a session: an `ao send`, a nudge, a
-- crew notice, the Tests tab's report. Text AO types into the pane reaches the
-- transcript looking exactly like the human typed it, so this - who wrote each
-- delivered body - is how a typed turn is told apart from a delivered one. The
-- author is decided at delivery, where AO still knows it: 'human' for the app's
-- send box and the Tests tab (a person's words, carried by AO), 'agent' for
-- another session's `ao send`, 'ao' for AO's own notices.
-- +goose StatementBegin
CREATE TABLE delivered_fingerprint (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id   TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id   TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    sha256       TEXT NOT NULL,
    bytes        INTEGER NOT NULL,
    trigger      TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL CHECK (author IN ('human', 'agent', 'ao')),
    delivered_at TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_delivered_fingerprint_session ON delivered_fingerprint (session_id);
-- +goose StatementEnd

-- How far capture has read each transcript. byte_offset is where the next pass
-- starts: the end of the last complete line, or the start of a human turn whose
-- window has not closed yet (the agent is still answering it). pending_carry
-- holds that turn's "before" context so a resumed pass does not need to re-read
-- what came before it. Advanced in the same transaction as the excerpts it
-- covers, so a crash can neither skip a turn nor store it twice.
-- +goose StatementBegin
CREATE TABLE learn_cursor (
    transcript_path TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL DEFAULT '',
    attribution     TEXT NOT NULL DEFAULT ''
        CHECK (attribution IN ('', 'pinned', 'hook', 'workspace')),
    byte_offset     INTEGER NOT NULL DEFAULT 0,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime      TIMESTAMP,
    pending_carry   TEXT NOT NULL DEFAULT '',
    human_turns     INTEGER NOT NULL DEFAULT 0,
    machine_turns   INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- One redacted human turn with its window. session_id carries no foreign key on
-- purpose: purging a session row must not erase what the human taught in it.
-- (transcript_path, turn_uuid) is unique, so re-reading a transcript after a
-- rewrite or a reset cursor never duplicates a turn.
-- +goose StatementBegin
CREATE TABLE learn_excerpt (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL DEFAULT '',
    transcript_path TEXT NOT NULL,
    turn_uuid       TEXT NOT NULL,
    turn_at         TIMESTAMP NOT NULL,
    source_class    TEXT NOT NULL,
    cwd             TEXT NOT NULL DEFAULT '',
    git_branch      TEXT NOT NULL DEFAULT '',
    before_json     TEXT NOT NULL DEFAULT '{}',
    human_text      TEXT NOT NULL,
    after_json      TEXT NOT NULL DEFAULT '{}',
    redactions_json TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMP NOT NULL,
    UNIQUE (transcript_path, turn_uuid)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_learn_excerpt_project ON learn_excerpt (project_id, turn_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE learn_excerpt;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE learn_cursor;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE delivered_fingerprint;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE prompt_fingerprint;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE session_transcript;
-- +goose StatementEnd
