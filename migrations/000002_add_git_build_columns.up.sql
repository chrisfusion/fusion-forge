-- SPDX-License-Identifier: GPL-3.0-or-later
ALTER TABLE venv_build
    ADD COLUMN build_type      VARCHAR(32)   NOT NULL DEFAULT 'requirements',
    ADD COLUMN repo_url        VARCHAR(2048),
    ADD COLUMN repo_ref        VARCHAR(255),
    ADD COLUMN entrypoint_file VARCHAR(500);
