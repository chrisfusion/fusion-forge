# Architecture

## Overview

fusion-forge is split into three processes that run in the same Kubernetes namespace:

| Process | Binary | Role |
|---|---|---|
| **Server** | `/server` | REST API — validates requests, writes DB, creates CIBuild CRs |
| **Operator** | `/operator` | Watches CIBuild CRs — creates Jobs, tracks completion, updates status |
| **Watcher** | `/watcher` | Polls registered GitWatcher CRs — triggers builds when a new version appears |
| **Builder** | `/forge-builder` | Runs inside each build Job — clones/installs, tars, uploads to fusion-index |

The server, operator, and watcher are compiled into the same Docker image (`fusion-forge`) but started with different entrypoints. The builder uses a separate image (`fusion-venv-builder` / `fusion-venv-builder-py310`) built on `python:3.1x-slim-bookworm`.

---

## Component diagram

```
 External caller                              GitWatcher CR (polled)
      │                                              │
      │  POST /api/v1/venvs        │  POST /api/v1/gitbuilds   │  POST /api/v1/appbuilds
      │  multipart/form-data       │  application/json         │  application/json
      ▼                                              ▼
┌──────────────────────────────────────────┐   ┌──────────────────────────┐
│              fusion-forge server          │   │    fusion-forge watcher   │
│                                          │   │                          │
│  ┌─────────────┐    ┌─────────────────┐  │   │  Reconciles GitWatcher   │
│  │  gin router  │    │  indexclient    │  │   │  CRs: polls remote HEAD,│
│  │  + auth mw   │    │  (HTTP client)  │  │   │  resolves version, calls│
│  └──────┬──────┘    └────────┬────────┘  │   │  buildtrigger on change  │
│         │                    │           │   └────────────┬─────────────┘
│  ┌──────▼──────┐    ┌────────▼────────┐  │                │
│  │  handlers   │    │  fusion-index   │  │                │
│  │ venvs/git/  │    │  artifact API   │  │                │
│  │ app/builds  │    └─────────────────┘  │                │
│  │ gitwatchers │                        │                │
│  └──────┬──────┘                        │                │
│         │ pgx/v5 (not gitwatchers)      │                │
│  ┌──────▼──────┐                        │                │
│  │ PostgreSQL  │                        │                │
│  │ venv_build  │◄───────────────────────┼────────────────┘ (shared buildtrigger package)
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
│  Git build (pyproject.toml / wheel):     │
│  • git clone --depth=1 <repo>            │
│  • validate structure (pyproject/src)    │
│  • pip wheel → pip install               │
│  • tar.gz /workspace/venv                │
│  • POST .tar.gz → fusion-index           │
│  • POST entrypoint_file (optional)       │
│                                          │
│  App build (metadata.yaml / requirements):│
│  • git clone --depth=1 <repo>            │
│  • optional: layer onto a base venvpack  │
│    from metadata.yaml's basedependencies │
│  • pip install -r requirements.txt       │
│  • copy project subdirs into             │
│    site-packages (must have __init__.py) │
│  • tar.gz /workspace/venv                │
│  • POST venvpack + main.py + metadata.yaml│
│    → fusion-index (3 files)              │
└──────────────────────────────────────────┘
```

---

## Request flow — requirements build

```
POST /api/v1/venvs
  1. Bind multipart form (name, version, requirements file, python_version)
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
  1. Bind JSON body (repo_url, repo_ref, name, version, metadata_source, project_dir, python_version, …)
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

## Request flow — app build

App builds derive **all** metadata (name, version, builder image, base dependencies URL, runner type) from the repository's `metadata.yaml` — the caller only supplies the repo coordinates.

```
POST /api/v1/appbuilds
  1. Bind JSON body (repo_url, repo_ref, project_dir)
  2. validateProjectDir → reject absolute paths and ".." escapes
  3. FetchAppMetadata: go-git depth-1 in-memory clone → read {project_dir}/metadata.yaml
       required: name, version, builderImage
       optional: basedependencies (URL), runner (string or structured object; only runner.type is stored)
  4. BuilderImageFor(meta.BuilderImage) → resolve container image from the builderImages ConfigMap
  5. GetVenvBuildByNameAndVersion → conflict check (DB)
  6. buildtrigger.TriggerAppBuild:
       FindOrCreateArtifact("app.{name}") → artifactID
       VersionExists → conflict check (registry)
       CreateVersion → reserve slot
       INSERT venv_build (build_type=app, runner, base_dependencies_url, …) → buildID
       kubectl create CIBuild "forge-app-{buildID}" (AppSource spec)
  7. Return 202 Accepted
```

Builder uploads three files per app build to the `app.{name}` artifact: the venvpack `.tar.gz`, `main.py`, and `metadata.yaml`.

---

## Operator reconcile loop

```
Watch CIBuild CR:

  Phase == "" (new):
    1. Create ConfigMap (configData files; no-op for git/app builds — empty configData)
    2. Create batch/v1 Job (builder pod)
    3. Set status.Phase = Building, status.JobName = "forge-job-{name}"

  Job Succeeded:
    1. Delete ConfigMap
    2. Set status.Phase = Succeeded, status.CompletedAt = now

  Job Failed:
    1. Delete ConfigMap
    2. Set status.Phase = Failed, status.Message = job failure message
```

The operator has no DB access — it is K8s-native only. It reads four `BUILDER_*` env vars (job/pod labels & annotations) to inject deployment-time metadata into every builder Job/Pod via `internal/jobbuilder`'s `BuildOptions`; system-managed labels always win over user-supplied ones.

---

## GitWatcher reconcile loop (watcher)

```
Watch GitWatcher CR:

  spec.enabled == false → phase = Disabled, requeue after 10× pollInterval
  phase == Disabled     → requeue after 10× pollInterval (until manually re-enabled)

  In-flight build check (status.lastBuildName != ""):
    read the CIBuild CR
      Pending/Building → requeue after jittered interval
      Succeeded → lastBuiltVersion = lastBuildVersion, consecutiveFailures = 0,
                  clear lastBuildName; requeue
      Failed    → look up the DB row by CIBuildName, cleanupFailedRow
                  (best-effort: DeleteVersion from fusion-index + DeleteVenvBuild)
                  increment consecutiveFailures; clear lastSeenCommit (forces a
                  fresh poll/retry); if consecutiveFailures >= maxFailures →
                  phase = Disabled; requeue

  Resolve token from the referenced K8s Secret (tokenSecretRef), if set
  FetchRemoteHEAD(repoURL, repoRef, token) — cheap ls-refs, no clone
  HEAD == lastSeenCommit → update lastCheckedAt, requeue

  resolveVersionAndMeta:
    app builds → FetchAppMetadata
    git builds → depends on metadataSource (manual/version/full) via FetchPyprojectMeta
  lastSeenCommit = head

  version == lastBuiltVersion → log "skipped" (version-change-driven, not commit-driven), requeue

  GetVenvBuildByNameAndVersion:
    SUCCESS           → skip
    PENDING/BUILDING  → skip
    FAILED            → cleanupFailedRow, then proceed

  BuilderImageFor(pythonVersion or meta.BuilderImage)
  TriggerGitBuild / TriggerAppBuild (internal/buildtrigger — same path the REST handlers use)
    ErrConflict → log "skipped", set lastBuiltVersion, requeue
    other error → increment consecutiveFailures; if >= maxFailures → Disabled; requeue

  Set lastBuildName, lastBuildVersion; requeue after jittered interval
```

**Jitter formula**: `pollInterval + (fnv32a(watcherName) % (pollInterval/4))` seconds — spreads polls across the interval window so all watchers don't hit git servers simultaneously.

The watcher's manager client excludes `corev1.Secret` from its informer cache (`ctrl.Options.Client.Cache.DisableFor`), so resolving `tokenSecretRef` only requires `secrets: get` RBAC rather than `list`/`watch`.

---

## Status lazy sync (GET)

The operator never writes to PostgreSQL. The server syncs status on demand:

```
GET /api/v1/venvs/{id}  (or /gitbuilds/{id}, /appbuilds/{id})
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

If a CIBuild CR is deleted outside the API (cluster wipe, manual `kubectl delete`), the DB row is stuck at PENDING/BUILDING forever since `syncStatusFromCR` swallows `NotFound` along with transient errors — `POST /api/v1/builds/zombie-cleanup` is the remedy.

---

## Bulk build maintenance

- **`DELETE /api/v1/builds`**: deletes builds matching `statuses` (`FAILED`/`SUCCESS` only — PENDING/BUILDING rejected with 422), `older_than`, and optional `build_type`. For FAILED builds, the orphaned fusion-index version is removed first (best-effort); CIBuild CRs are deleted best-effort for all matched builds. Capped at 1000 rows per call.
- **`POST /api/v1/builds/zombie-cleanup`**: finds PENDING/BUILDING rows whose CIBuild CR no longer exists in the cluster, removes the fusion-index version (best-effort), and deletes the DB row. Requires `older_than`; capped at 1000 rows inspected per call.

Both live in `internal/api/handlers/builds.go` (`BuildsHandler`) since they cut across all three build types, and both return `{"deleted": [...ids], "failed": [{"id", "error"}, ...]}` (`dto.BulkDeleteResponse`).

---

## Database schema

Single table: `venv_build` (name predates git/app builds but still holds every build type)

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
| `ci_build_name` | `VARCHAR(255)` | `forge-venv-{id}` / `forge-git-{id}` / `forge-app-{id}`; nullable until set |
| `build_type` | `VARCHAR(20)` | `requirements` / `git` / `app` |
| `repo_url` | `VARCHAR(2048)` | git and app builds |
| `repo_ref` | `VARCHAR(255)` | git and app builds |
| `entrypoint_file` | `VARCHAR(500)` | optional; relative to project root |
| `metadata_source` | `VARCHAR(32)` | `manual` / `version` / `full`; git builds only |
| `project_dir` | `VARCHAR(500)` | optional monorepo subdirectory |
| `python_version` | `VARCHAR(10)` | `"3.10"` / `"3.12"` (default); requirements and git builds |
| `runner` | `VARCHAR(255)` | app builds; extracted from `metadata.yaml`'s `runner.type` |
| `base_dependencies_url` | `TEXT` | app builds; optional URL from `metadata.yaml`'s `basedependencies` |
| `created_at` | `TIMESTAMPTZ` | |
| `updated_at` | `TIMESTAMPTZ` | |

Migrations live in `migrations/` (embedded via `//go:embed` + `source/iofs`) and run automatically at server startup.

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
  buildType           string        # "requirements" | "git" | "app"
  configData          map[string]string   # filename → content (requirements builds)
  gitSource           GitSourceSpec?      # git builds only
    url               string
    ref               string
    entrypointFile    string
    projectDir        string        # monorepo subdirectory
    tokenSecretRef     SecretKeyRef?     # private repo auth
  appSource           AppSourceSpec?      # app builds only
    url               string
    ref               string
    projectDir        string
    baseDependenciesURL string
    tokenSecretRef     SecretKeyRef?
  env                 []EnvVar      # ARTIFACT_ID, ARTIFACT_VERSION, VENV_NAME, …

CIBuildStatus
  phase               Pending | Building | Succeeded | Failed
  jobName             string        # forge-job-{name}
  configMapName       string        # forge-cfg-{name}; cleared on terminal state
  message             string        # failure reason
  startedAt           time
  completedAt         time
```

The operator adds `GIT_REPO_URL`, `GIT_REF`, `GIT_PROJECT_DIR`, `ENTRYPOINT_FILE`, and (when `tokenSecretRef` is set) `GIT_TOKEN` (via `valueFrom.secretKeyRef`, never a literal on the spec) as env vars from `gitSource`/`appSource` so the builder binary can read them without needing the CRD schema itself.

---

## GitWatcher CRD

Group: `build.fusion-platform.io` / Version: `v1alpha1` · short name: `gw`

```
GitWatcherSpec
  repoURL          string
  repoRef          string          # default "main"
  buildType        string          # "git" | "app"
  enabled          bool            # default true
  metadataSource   string          # "manual" | "version" | "full" — git builds only
  name             string          # required for manual/version; ignored for full
  version          string          # required for manual; ignored otherwise
  pythonVersion    string          # builder image key; ignored for app builds
  entrypointFile   string          # optional; ignored for app builds
  projectDir       string          # optional monorepo subdirectory
  description      string
  tokenSecretRef   SecretKeyRef?   # optional — private repos

GitWatcherStatus
  phase               Active | Disabled
  lastSeenCommit      string        # HEAD SHA from last successful poll
  lastBuiltVersion    string        # version from last completed build
  lastBuildName       string        # in-flight CIBuild CR name (empty when idle)
  lastBuildVersion    string        # version of the in-flight build
  consecutiveFailures int
  lastCheckedAt       time
  lastError           string
  message             string
```

The GitWatcher CRD is **not Helm-managed** — apply it manually before `helm upgrade` to avoid field-manager conflicts:

```bash
kubectl apply -f config/crd/bases/build.fusion-platform.io_gitwatchers.yaml
```

`internal/api/handlers/gitwatchers.go` (`GitWatcherHandler`) provides full CRUD over `/api/v1/gitwatchers` — K8s CR only, no DB row. `POST` always does a live `FetchRemoteHEAD` pre-flight; `PUT` only re-checks when `repo_url` or `repo_ref` changes.

---

## metadata_source modes (git builds)

| Mode | name source | version source | Server action |
|---|---|---|---|
| `manual` (default) | request body | request body | none — uses provided values |
| `version` | request body | `pyproject.toml` | in-memory clone, parse `[project].version` |
| `full` | `pyproject.toml` | `pyproject.toml` | in-memory clone, parse `[project].name` + `[project].version` |

The in-memory clone uses `go-git` (no `git` binary required on the server). It tries `refs/tags/{ref}` first, then `refs/heads/{ref}`. Dynamic versions (`dynamic = ["version"]` in pyproject.toml) are not supported.

App builds always read `name`, `version`, `builderImage`, `basedependencies`, and `runner` from `metadata.yaml` — there is no `metadata_source` equivalent for app builds.

---

## Builder image selection

All three build types resolve their builder container image from a single Helm-managed `{release-name}-builder-images` ConfigMap (`server.config.builderImages` in `values.yaml`), loaded at server startup:

- Requirements and git builds: key is the `python_version` request field (`"3.10"`, `"3.12"`, also aliased as `"python3.10"`/`"python3.12"`)
- App builds: key is the `builderImage` field read from `metadata.yaml`

`Config.BuilderImageFor(key)` returns the resolved image ref or an error (surfaced as `400`/`422`) if the key is unknown.

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
     (uses GIT_TOKEN via a git credential helper, username "oauth2", if set)
  2. Resolve projectRoot = /workspace/src[/{project_dir}]
  3. Validate structure (pyproject.toml, src/, entrypoint_file)
  4. python3 -m venv /workspace/venv
  5. pip wheel --no-cache-dir -w /workspace/dist {projectRoot}   (never --no-deps: build-system deps like hatchling/flit must resolve)
  6. pip install --no-cache-dir {wheel}
  7. tar czf {archive} -C /workspace venv
  8. POST {archive} → fusion-index
  9. POST {entrypoint_file} → fusion-index  (if configured)
```

## Builder pipeline — app build

```
/workspace/
  src/          ← git clone lands here (metadata.yaml, requirements.txt, main.py, project subdirs)
  venv/         ← python3 -m venv, optionally seeded from a base venvpack
  {name}-{version}.tar.gz  ← archive of venv/ only (clean, no project source at top level)

Steps:
  1. git clone --single-branch --depth=1 --branch {ref} {url} /workspace/src
  2. If basedependencies is set: download and extract the base venvpack into /workspace/venv
     else: python3 -m venv /workspace/venv
  3. pip install --no-cache-dir -r /workspace/src/requirements.txt
  4. Copy every project subdirectory (e.g. internals/) from /workspace/src into
     venv/lib/pythonX.Y/site-packages/ so main.py can `import` them as packages
     (each subdirectory must have __init__.py, or it becomes a namespace package
     with __file__ == None and path resolution breaks at runtime)
  5. tar czf {archive} -C /workspace venv
  6. POST {archive} + main.py + metadata.yaml → fusion-index (3 uploads)
```

> **Naming note**: in `builder/main.go`, `buildFromGit` is actually the pyproject.toml/pip-wheel path described above, and `buildFromApp` is the metadata.yaml/pip-install path. The names are inverted relative to what they do; a rename to `buildFromToml`/`buildFromMetadata` is planned but deferred until the REST API layer and BFF clients are audited (the `BUILD_TYPE` env var values `"git"`/`"app"` must stay unchanged either way).

---

## Security

- **Builder pod**: `allowPrivilegeEscalation: false`, `capabilities: drop: ALL`, pod-level `runAsNonRoot`/`runAsUser`/`seccompProfile` (defaults: `true`/`1000`/`RuntimeDefault`, configurable per-deployment). `readOnlyRootFilesystem` is intentionally not set because the builder writes to `/workspace`.
- **Server, operator, and watcher pods**: same baseline plus `readOnlyRootFilesystem: true`.
- **Auth**: optional K8s `TokenReview` — set `AUTH_ENABLED=true` and configure `AUTH_ALLOWED_SA` to restrict callers to specific service accounts.
- **project_dir validation**: the server rejects absolute paths and any path containing `..` components to prevent directory traversal at clone time.
- **Private repositories**: `tokenSecretRef` (on `GitWatcher.spec`, `CreateGitWatcherRequest`, or the CIBuild `gitSource`/`appSource`) references a same-namespace K8s Secret. It authenticates both legs independently — the watcher/server's own metadata pre-fetch (`FetchAppMetadata`/`FetchPyprojectMeta`) and the builder Job's git clone (`GIT_TOKEN` env var → credential helper, username fixed to `oauth2`) — both must be wired for a private repo to actually build.
