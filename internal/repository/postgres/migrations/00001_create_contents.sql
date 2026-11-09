-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- array_to_string is only STABLE, so generated columns need an IMMUTABLE wrapper.
-- +goose StatementBegin
CREATE FUNCTION immutable_array_to_string(arr TEXT[], sep TEXT) RETURNS TEXT
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS
$$ SELECT array_to_string(arr, sep) $$;
-- +goose StatementEnd

CREATE TABLE contents (
    id               BIGSERIAL PRIMARY KEY,
    provider         TEXT             NOT NULL,
    external_id      TEXT             NOT NULL,
    title            TEXT             NOT NULL,
    type             TEXT             NOT NULL CHECK (type IN ('video', 'text')),
    views            BIGINT           NOT NULL DEFAULT 0 CHECK (views >= 0),
    likes            BIGINT           NOT NULL DEFAULT 0 CHECK (likes >= 0),
    duration_sec     INTEGER          NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
    reading_time     INTEGER          NOT NULL DEFAULT 0 CHECK (reading_time >= 0),
    reactions        BIGINT           NOT NULL DEFAULT 0 CHECK (reactions >= 0),
    comments         BIGINT           NOT NULL DEFAULT 0 CHECK (comments >= 0),
    tags             TEXT[]           NOT NULL DEFAULT '{}',
    published_at     TIMESTAMPTZ      NOT NULL,
    score            DOUBLE PRECISION NOT NULL,
    score_updated_at TIMESTAMPTZ      NOT NULL,
    raw_payload      JSONB,
    search_vector    TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', title), 'A') ||
        setweight(to_tsvector('simple', immutable_array_to_string(tags, ' ')), 'B')
    ) STORED,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    CONSTRAINT contents_provider_external_id_key UNIQUE (provider, external_id)
);

CREATE INDEX contents_search_vector_idx ON contents USING GIN (search_vector);
CREATE INDEX contents_title_trgm_idx ON contents USING GIN (title gin_trgm_ops);
CREATE INDEX contents_score_idx ON contents (score DESC, id);
CREATE INDEX contents_type_score_idx ON contents (type, score DESC, id);

-- +goose Down
DROP TABLE contents;
DROP FUNCTION immutable_array_to_string(TEXT[], TEXT);
