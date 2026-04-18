ALTER TABLE venv_build
    ADD COLUMN metadata_source VARCHAR(32) NOT NULL DEFAULT 'manual';
