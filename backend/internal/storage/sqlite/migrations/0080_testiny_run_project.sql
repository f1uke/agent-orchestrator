-- Summary: each linked Testiny run carries its own Testiny project, and a
-- project's config says only whether it uses Testiny.
--
-- Run ids are global in Testiny, so one task may hold runs from several
-- Testiny projects. A link made before this migration has project_id 0 until
-- the service reads its run again and fills it in.
--
-- projects.config: a non-empty testinyProject becomes usesTestiny: true, and
-- testinyProject is removed from every config.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE testiny_run_link ADD COLUMN project_id INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_run_link ADD COLUMN project_key TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_run_link ADD COLUMN project_name TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE projects
SET config = json_set(config, '$.usesTestiny', json('true'))
WHERE config IS NOT NULL AND json_valid(config)
  AND json_type(config, '$.testinyProject') = 'text'
  AND trim(json_extract(config, '$.testinyProject')) <> '';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE projects
SET config = json_remove(config, '$.testinyProject')
WHERE config IS NOT NULL AND json_valid(config)
  AND json_type(config, '$.testinyProject') IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE testiny_run_link DROP COLUMN project_name;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_run_link DROP COLUMN project_key;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE testiny_run_link DROP COLUMN project_id;
-- +goose StatementEnd
