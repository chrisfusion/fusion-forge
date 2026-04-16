-- fusion-forge venv_build schema (Go implementation).
-- If migrating from the Java version: DROP TABLE venv_build; DROP SEQUENCE venv_build_seq;

CREATE TABLE venv_build (
    id                     BIGSERIAL    PRIMARY KEY,
    name                   VARCHAR(255) NOT NULL,
    version                VARCHAR(50)  NOT NULL,
    description            TEXT,
    status                 VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    creator_id             VARCHAR(500),
    creator_email          VARCHAR(500),
    -- fusion-index artifact reference (resolved before build starts)
    index_artifact_id      BIGINT,
    index_artifact_version VARCHAR(50),
    -- K8s CIBuild CR name (set after DB insert using the generated id)
    ci_build_name          VARCHAR(255),
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_venv_build_name_version UNIQUE (name, version)
);
