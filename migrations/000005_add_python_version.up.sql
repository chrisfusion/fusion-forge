ALTER TABLE venv_build
    ADD COLUMN python_version VARCHAR(10) NOT NULL DEFAULT '3.12';
