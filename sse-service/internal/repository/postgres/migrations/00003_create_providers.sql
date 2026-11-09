-- +goose Up
CREATE TABLE providers (
    id         BIGSERIAL   PRIMARY KEY,
    name       TEXT        NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9_-]{1,49}$'),
    url        TEXT        NOT NULL CHECK (url ~* '^https?://'),
    format     TEXT        NOT NULL CHECK (format IN ('json', 'xml')),
    enabled    BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT providers_name_key UNIQUE (name)
);

-- The two providers of the case study, previously configured in code.
INSERT INTO providers (name, url, format) VALUES
    ('provider1', 'https://raw.githubusercontent.com/WEG-Technology/mock/refs/heads/main/v2/provider1', 'json'),
    ('provider2', 'https://raw.githubusercontent.com/WEG-Technology/mock/refs/heads/main/v2/provider2', 'xml');

ALTER TABLE contents
    ADD CONSTRAINT contents_provider_fkey FOREIGN KEY (provider) REFERENCES providers (name);

-- +goose Down
ALTER TABLE contents DROP CONSTRAINT contents_provider_fkey;
DROP TABLE providers;
