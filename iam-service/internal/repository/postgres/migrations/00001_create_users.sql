-- +goose Up
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT        NOT NULL CHECK (username = lower(username) AND length(username) BETWEEN 3 AND 32),
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_username_key UNIQUE (username)
);

-- +goose Down
DROP TABLE users;
