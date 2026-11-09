-- +goose Up
CREATE TABLE sync_runs (
    id          BIGSERIAL PRIMARY KEY,
    provider    TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    status      TEXT        NOT NULL CHECK (status IN ('success', 'failed')),
    fetched     INTEGER     NOT NULL DEFAULT 0,
    upserted    INTEGER     NOT NULL DEFAULT 0,
    error       TEXT
);

CREATE INDEX sync_runs_provider_started_at_idx ON sync_runs (provider, started_at DESC);

-- +goose Down
DROP TABLE sync_runs;
