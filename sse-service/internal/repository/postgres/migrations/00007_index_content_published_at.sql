-- +goose Up
-- Supports the publication date filter (published_at >= from AND published_at < to). Combined
-- with another filter, PostgreSQL can intersect it with that filter's index (BitmapAnd).
CREATE INDEX contents_published_at_idx ON contents (published_at);

-- +goose Down
DROP INDEX contents_published_at_idx;
