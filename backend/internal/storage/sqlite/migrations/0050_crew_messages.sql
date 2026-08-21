-- Summary: one row per CREW-TO-CREW message attempt - dev telling qa about a
-- commit, qa telling dev what a run found - accepted or refused.
--
-- Two agents that can both run and both message each other will talk forever and
-- spend real money with nobody watching, so the conversation is CAPPED rather
-- than trusted. Every message must name what it is about (a commit SHA or a
-- smoke case id), and a subject is allowed only CappedRepeat messages in one
-- direction before the next is refused. A second cap on messages per hour per
-- crew catches a loop that escapes the first by varying the subject.
--
-- Refused attempts are stored TOO, and that is the point of the refused_reason
-- column: the refusal is the escalation signal. A crew whose latest message was
-- refused has a conversation that stopped, so the card goes to NEEDS YOU - and
-- it clears itself the moment a later message goes through, which happens as
-- soon as the members move on to a new commit or a new case.
--
-- Nothing here can fire for a solo session: it has no crew, so it has no
-- crewmate to address and never reaches this table.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE crew_message (
    id             TEXT PRIMARY KEY,
    crew_id        TEXT NOT NULL,
    project_id     TEXT NOT NULL DEFAULT '',
    from_session   TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    to_session     TEXT NOT NULL,
    subject        TEXT NOT NULL,
    refused_reason TEXT NOT NULL DEFAULT '',   -- '' when the message was delivered or queued
    created_at     TIMESTAMP NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crew_message_thread ON crew_message (crew_id, subject, from_session, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crew_message_sender ON crew_message (from_session, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
-- A refusal moves the sender's card to NEEDS YOU, and a later message moves it
-- back, so every attempt fans out a session_updated event on the SENDER's row.
CREATE TRIGGER crew_message_cdc_insert
AFTER INSERT ON crew_message
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.from_session, 'session_updated',
        json_object('id', NEW.from_session, 'crewMessageId', NEW.id, 'crewMessageRefused', NEW.refused_reason <> ''),
        NEW.created_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS crew_message_cdc_insert;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS crew_message;
-- +goose StatementEnd
