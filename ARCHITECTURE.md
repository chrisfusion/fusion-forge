# Architecture

## Overview

fusion-forge is split into three processes that run in the same Kubernetes namespace:

| Process | Binary | Role |
|---|---|---|
| **Server** | `/server` | REST API — validates requests, writes DB, creates CIBuild CRs |
| **Operator** | `/operator` | Watches CIBuild CRs — creates Jobs, tracks completion, updates status |
| **Builder** | `/forge-builder` | Runs inside each build Job — clones/installs, tars, uploads to fusion-index |

The server and operator are compiled into the same Docker image (`fusion-forge`) but started with different entrypoints. The builder uses a separate image (`fusion-venv-builder`) built on `python:3.12-slim-bookworm`.

---

## Component diagram

```
 External caller
      │
      │  POST /api/v1/venvs          POST /api/v1/gitbuilds
      │  multipart/form-data         application/json
      ▼
┌──────────────────────────────────────────┐
│              fusion-forge server          │
│                                          │
│  ┌─────────────┐    ┌─────────────────┐  │
│  │  gin router  │    │  indexclient    │  │
│  │  + auth mw   │    │  (HTTP client)  │  │
│  └──────┬──────┘    └────────┬────────┘  │
│         │                    │           │
│  ┌──────▼──────┐    ┌────────▼────────┐  │
│  │  handler    │    │  fusion-index   │  │
│  │  venvs.go   │    │  artifact API   │  │
│  │  gitbuilds  │    └─────────────────┘  │
│  └──────┬──────┘                        │
│         │ pgx/v5                        │
│  ┌──────▼──────┐                        │
│  │ PostgreSQL  │                        │
│  │ venv_build  │                        │
│  └─────────────┘                        │
│         │ ctrl.Create(CIBuild CR)       │
└─────────┼────────────────────────────────┘
          │
          ▼ CIBuild CR
┌──────────────────────────────────────────┐
│           fusion-forge operator           │
│                                          │
│  Reconciler watches CIBuild CRs:         │
│  • Creates ConfigMap + Job               │
│  • Watches Job completion                │
│  • Sets CIBuild.Status.Phase             │
│  • Deletes ConfigMap on terminal state   │
└────────────────┬─────────────────────────┘
                 │ batch/v1 Job
                 ▼
┌──────────────────────────────────────────┐
│           builder pod                     │
│                                          │
│  Requirements build:                     │
│  • pip install -r requirements.txt       │
│  • tar.gz /workspace/venv                │
│  • POST .tar.gz → fusion-index           │
│                                          │
│  Git build:                              │
│  • git clone --depth=1 <repo>            │
│  • validate structure (pyproject/src)    │
│  • pip wheel → pip install               │
│  • tar.gz /workspace/venv                │
│  • POST .tar.gz → fusion-index           │
│  • POST entrypoint_file (optional)       │
└──────────────────────────────────────────┘
```

---

## Request flow — requirements build

```
POST /api/v1/venvs
  1. Bind multipart form (name, version, requirements file)
  2. Validate requirements.txt against forge-rules.yaml
  3. FindOrCreateArtifact("venv.{name}") → fusion-index → artifactID
  4. VersionExists(artifactID, version) → conflict check
  5. CreateVersion(artifactID, version) → reserve slot in registry
  6. INSERT venv_build (status=PENDING, build_type=requirements) → buildID
  7. UPDATE venv_build SET ci_build_name = "forge-venv-{buildID}"
  8. kubectl create CIBuild "forge-venv-{buildID}" (configData: requirements.txt)
  9. Return 202 Accepted with the DB row
```

## Request flow — git build

```
POST /api/v1/gitbuilds
  1. Bind JSON body (repo_url, repo_ref, name, version, metadata_source, project_dir, …)
  2. validateProjectDir → reject absolute paths and ".." escapes
  3. normalizeMetadataSource → enforce required fields per mode
  4. resolveMetadata (if metadata_source != "manual"):
       go-git depth-1 in-memory clone → read {project_dir}/pyproject.toml
       populate name and/or version from [project] table
  5. FindOrCreateArtifact("venv.{name}") → artifactID
  6. VersionExists → conflict check
  7. CreateVersion → reserve slot
  8. INSERT venv_build (build_type=git, repo_url, repo_ref, project_dir, …) → buildID
  9. UPDATE venv_build SET ci_build_name = "forge-git-{buildID}"
 10. kubectl create CIBuild "forge-git-{buildID}" (GitSource spec, empty configData)
 11. Return 202 Accepted
```

## Operator reconcile loop

```
Watch CIBuild CR:

  Phase == "" (new):
    1. Create ConfigMap (configData files; no-op for git builds)
    2. Create batch/v1 Job (builder pod)
    3. Set status.Phase = Building, status.JobName = "forge-job-{name}"

  Job Succeeded:
    1. Delete ConfigMap
    2. Set status.Phase = Succeeded, status.CompletedAt = now

  Job Failed:
    1. Delete ConfigMap
    2. Set status.Phase = Failed, status.Message = job failure message
```

## Status lazy sync (GET)

The operator never writes to PostgreSQL. The server syncs status on demand:

```
GET /api/v1/venvs/{id}  (or /gitbuilds/{id})
  1. Read DB row
  2. If status is PENDING or BUILDING:
       Read CIBuild CR phase
       If phase changed → UPDATE venv_build SET status = mapped_status
  3. Return (possibly updated) row
```

Phase → status mapping:

| CIBuild phase | DB status |
|---|---|
| `Building` | `BUILDING` |
| `Succeeded` | `SUCCESS` |
| `Failed` | `FAILED` |

---

## Database schema

Single table: `venv_build`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL PK` | |
| `name` | `VARCHAR(255)` | package name |
| `version` | `VARCHAR(50)` | semver |
| `description` | `TEXT` | nullable |
| `status` | `VARCHAR(20)` | `PENDING` / `BUILDING` / `SUCCESS` / `FAILED` |
| `creator_id` | `VARCHAR(255)` | K8s SA username; nullable |
| `creator_email` | `VARCHAR(255)` | nullable (requirements builds only) |
| `index_artifact_id` | `BIGINT` | fusion-index artifact ID; nullable |
| `index_artifact_version` | `VARCHAR(50)` | nullable |
| `ci_build_name` | `VARCHAR(255)` | `forge-venv-{id}` or `forge-git-{id}`; nullable until set |
| `build_type` | `VARCHAR(20)` | `requirements` or `git` |
| `repo_url` | `VARCHAR(2048)` | git builds only |
| `repo_ref` | `VARCHAR(255)` | git builds only |
| `entrypoint_file` | `VARCHAR(500)` | optional; relative to project root |
| `metadata_source` | `VARCHAR(32)` | `manual` / `version` / `full` |
| `project_dir` | `VARCHAR(500)` | optional monorepo subdirectory |
| `created_at` | `TIMESTAMPTZ` | |
| `updated_at` | `TIMESTAMPTZ` | |

Migrations live in `migrations/` and run automatically at server startup.

---

## CIBuild CRD

Group: `build.fusion-platform.io` / Version: `v1alpha1`

```
CIBuildSpec
  builderImage        string        # container image for the build Job
  indexBackendURL     string        # fusion-index base URL
  artifactName        string        # display name
  artifactVersion     string        # semver
  description         string
  buildType           string        # "requirements" | "git"
  configData          map[string]string   # filename → content (requirements builds)
  gitSource           GitSourceSpec?      # git builds only
    url               string
    ref               string
    entrypointFile    string
    projectDir        string        # monorepo subdirectory
  env                 []EnvVar      # ARTIFACT_ID, ARTIFACT_VERSION, VENV_NAME, …

CIBuildStatus
  phase               Pending | Building | Succeeded | Failed
  jobName             string        # forge-job-{name}
  configMapName       string        # forge-cfg-{name}; cleared on terminal state
  message             string        # failure reason
  startedAt           time
  completedAt         time
```

The operator adds `GIT_REPO_URL`, `GIT_REF`, `GIT_PROJECT_DIR`, `ENTRYPOINT_FILE` as env vars from `gitSource` so the builder binary can read them without needing the CRD schema itself.

---

## Builder pipeline — git build

```
/workspace/
  src/          ← git clone lands here
    (repo root)
      project_dir/    ← if set, this becomes "projectRoot"
        pyproject.toml
        src/
        app.py          ← entrypoint_file (relative to projectRoot)
  venv/         ← python3 -m venv
  dist/         ← pip wheel output
  {name}-{version}.tar.gz  ← archive of venv/

Steps:
  1. git clone --single-branch --depth=1 --branch {ref} {url} /workspace/src
  2. Resolve projectRoot = /workspace/src[/{project_dir}]
  3. Validate structure (pyproject.toml, src/, entrypoint_file)
  4. python3 -m venv /workspace/venv
  5. pip wheel --no-cache-dir -w /workspace/dist {projectRoot}
  6. pip install --no-cache-dir {wheel}
  7. tar czf {archive} -C /workspace venv
  8. POST {archive} → fusion-index
  9. POST {entrypoint_file} → fusion-index  (if configured)
```

---

## metadata_source modes

| Mode | name source | version source | Server action |
|---|---|---|---|
| `manual` (default) | request body | request body | none — uses provided values |
| `version` | request body | `pyproject.toml` | in-memory clone, parse `[project].version` |
| `full` | `pyproject.toml` | `pyproject.toml` | in-memory clone, parse `[project].name` + `[project].version` |

The in-memory clone uses `go-git` (no `git` binary required on the server). It tries `refs/tags/{ref}` first, then `refs/heads/{ref}`. Dynamic versions (`dynamic = ["version"]` in pyproject.toml) are not supported.

---

## Security

- **Builder pod**: `allowPrivilegeEscalation: false`, `capabilities: drop: ALL`. `readOnlyRootFilesystem` is intentionally not set because the builder writes to `/workspace`.
- **Server and operator pods**: same baseline plus `readOnlyRootFilesystem: true`.
- **Auth**: optional K8s `TokenReview` — set `AUTH_ENABLED=true` and configure `AUTH_ALLOWED_SA` to restrict callers to specific service accounts.
- **project_dir validation**: the server rejects absolute paths and any path containing `..` components to prevent directory traversal at clone time.
