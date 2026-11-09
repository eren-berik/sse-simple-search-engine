-- +goose Up
-- Providers can be renamed and deleted: contents follow a rename and are removed with their provider.
ALTER TABLE contents DROP CONSTRAINT contents_provider_fkey;
ALTER TABLE contents
    ADD CONSTRAINT contents_provider_fkey FOREIGN KEY (provider) REFERENCES providers (name)
        ON UPDATE CASCADE ON DELETE CASCADE;

-- +goose Down
ALTER TABLE contents DROP CONSTRAINT contents_provider_fkey;
ALTER TABLE contents
    ADD CONSTRAINT contents_provider_fkey FOREIGN KEY (provider) REFERENCES providers (name);
