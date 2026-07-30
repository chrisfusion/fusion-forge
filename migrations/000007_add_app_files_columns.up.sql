-- SPDX-License-Identifier: GPL-3.0-or-later
-- Adds app-build columns for the metadata.yaml `files` key: file_upload_mode
-- ("legacy"/"auto"/"list", NULL for non-app builds) and files (the resolved
-- whitelist, only populated in "list" mode).
ALTER TABLE venv_build ADD COLUMN file_upload_mode VARCHAR(20);
ALTER TABLE venv_build ADD COLUMN files TEXT[];
