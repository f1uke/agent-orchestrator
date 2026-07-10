-- +goose Up
-- system flags provider-generated system notes (e.g. GitLab's "changed this
-- line in version N of the diff", carried on the API as system:true) so they can
-- be rendered as a de-emphasized activity line instead of a second user comment.
-- Defaults to 0 so existing rows and non-GitLab comments remain plain comments.
-- +goose StatementBegin
ALTER TABLE pr_comment ADD COLUMN system INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pr_comment DROP COLUMN system;
-- +goose StatementEnd
