-- +goose Up
-- Supports the tag filter (tags @> ARRAY['go']).
CREATE INDEX contents_tags_idx ON contents USING GIN (tags);

-- +goose Down
DROP INDEX contents_tags_idx;
