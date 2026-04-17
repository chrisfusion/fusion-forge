-- SPDX-License-Identifier: GPL-3.0-or-later
ALTER TABLE venv_build
    DROP COLUMN IF EXISTS build_type,
    DROP COLUMN IF EXISTS repo_url,
    DROP COLUMN IF EXISTS repo_ref,
    DROP COLUMN IF EXISTS entrypoint_file;
