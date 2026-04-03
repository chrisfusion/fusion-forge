CREATE SEQUENCE venv_build_seq START WITH 1 INCREMENT BY 50;

CREATE TABLE venv_build (
    id                   BIGINT       PRIMARY KEY DEFAULT nextval('venv_build_seq'),
    name                 VARCHAR(255) NOT NULL,
    version              VARCHAR(255) NOT NULL,
    description          TEXT,
    status               VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    creator_id           VARCHAR(500),
    creator_email        VARCHAR(500),
    index_backend_job_id BIGINT,
    k8s_job_name         VARCHAR(255),
    k8s_configmap_name   VARCHAR(255),
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_venv_build_name_version UNIQUE (name, version)
);
