-- Summary: which BUILD a piece of evidence saw.
--
-- A result already records a commit (`ao smoke record --sha`), and a commit is
-- not an installed binary. `xcodebuild test -destination <udid>` builds and
-- installs the app target as part of running tests, so the binary on the device
-- can change under a session that never asked for it - and a stale install is
-- right commit, WRONG BYTES, which the sha column cannot express. Screenshots
-- taken either side of that reinstall look identical and get filed under one
-- verdict.
--
-- The value is the app's identity as `ao sim shot` read it off the device at
-- the moment of capture: bundle id, version, and a digest of the installed
-- bytes. It arrives INSIDE the PNG (an `ao-build` tEXt chunk), so it survives
-- the file being downloaded, moved and dragged into the Tests tab by a person
-- who was never told there was anything to bring along - which is why this is a
-- column filled by the upload path rather than a flag somebody has to pass.
--
-- '' means the capture could not say - a picture from somewhere else, an older
-- `ao sim shot`, a device whose app could not be read. Never confuse it with
-- "the same build as the row above".
-- +goose Up
-- +goose StatementBegin
ALTER TABLE smoke_evidence ADD COLUMN build TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE smoke_evidence DROP COLUMN build;
-- +goose StatementEnd
