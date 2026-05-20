-- SPDX-License-Identifier: GPL-3.0-or-later
-- Adds app-build columns: runner (the application framework, e.g. "streamlit") and
-- base_dependencies_url (optional URL to a base venvpack to layer requirements on top of).
ALTER TABLE venv_build ADD COLUMN runner VARCHAR(255);
ALTER TABLE venv_build ADD COLUMN base_dependencies_url VARCHAR(2048);
