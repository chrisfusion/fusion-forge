# Changelog

All notable changes to fusion-forge are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### Added
- **REST CRUD for GitWatcher CRs** (`GET/POST /api/v1/gitwatchers`, `GET/PUT/DELETE /api/v1/gitwatchers/:name`): create, list (paginated), get, full-spec update, and delete GitWatcher CRs directly via the REST API; `POST` validates repo reachability via `FetchRemoteHEAD` pre-flight; responses use nested `spec`/`status` shape; `tokenSecretRef` name/key included in responses
- `internal/api/dto/gitwatcher_dto.go`: `CreateGitWatcherRequest`, `UpdateGitWatcherRequest`, `GitWatcherResponse`, `GitWatcherPageResponse`, `ToGitWatcherResponse`
- `internal/api/handlers/gitwatchers.go`: `GitWatcherHandler` with `List`, `Create`, `Get`, `Update`, `Delete`; validates `build_type`, `metadata_source`, and `python_version` enums; reads K8s Secret for token pre-flight
- `deployment/templates/rbac.yaml`: server role extended with `gitwatchers` (create/get/list/watch/update/patch/delete), `gitwatchers/status` (get), and `secrets` (get) permissions


- **GitOps watcher** (`cmd/watcher`): new binary that periodically polls registered `GitWatcher` CRs; triggers git or app builds whenever a new artifact version is detected; supports token-authenticated private repos, configurable poll interval with per-repo jitter to avoid thundering-herd, retry-with-cleanup on build failure (globally configurable max failures, default 2), and auto-disables the watcher on threshold breach
- `api/v1alpha1/gitwatcher_types.go`: `GitWatcher` CRD with `spec.buildType` (`git`|`app`), `spec.metadataSource`, `spec.tokenSecretRef` for K8s Secret-backed PAT, `spec.enabled` toggle, and full status tracking (`phase`, `lastSeenCommit`, `lastBuiltVersion`, `lastBuildName`, `consecutiveFailures`)
- `config/crd/bases/build.fusion-platform.io_gitwatchers.yaml`: hand-crafted CRD YAML for the GitWatcher type
- `internal/controller/gitwatcher_controller.go`: controller-runtime reconciler that polls remote HEAD, resolves version, checks DB for existing builds, triggers via `buildtrigger`, and handles success/failure lifecycle
- `internal/buildtrigger/trigger.go`: shared package extracted from the REST handlers; `TriggerGitBuild` and `TriggerAppBuild` encapsulate the full FindOrCreateArtifact → CreateVersion → DB insert → CIBuild CR creation flow; `ErrConflict` sentinel for 409-class conditions
- `internal/gitutil/remote.go`: `FetchRemoteHEAD` — cheap HEAD SHA detection via go-git ls-refs (no clone)
- Token authentication added to `gitutil.FetchPyprojectMeta` and `gitutil.FetchAppMetadata`; handlers pass `""` (no token), watcher passes the PAT resolved from the K8s Secret
- `internal/db`: `GetVenvBuildByCIBuildName` and `DeleteVenvBuild` query functions for watcher failure cleanup
- `internal/indexclient`: `DeleteVersion` — removes an orphaned version from fusion-index after a failed build (best-effort, 404 treated as success)
- `internal/config`: `WatcherPollInterval` (`WATCHER_POLL_INTERVAL`, default 60 s) and `WatcherMaxFailures` (`WATCHER_MAX_FAILURES`, default 2)
- Helm: `watcher` section in `values.yaml`; `watcher-configmap.yaml`, `watcher-deployment.yaml` templates; watcher `ServiceAccount`, `Role`, `RoleBinding` in existing templates; `fusion-forge.watcherSAName` helper

### Changed
- `internal/api/handlers/gitbuilds.go` Create: delegates trigger logic to `buildtrigger.TriggerGitBuild` (DRY)
- `internal/api/handlers/appbuilds.go` Create: delegates trigger logic to `buildtrigger.TriggerAppBuild` (DRY)


- **App build type** (`POST /api/v1/appbuilds`): new builder that clones a git repository containing `metadata.yaml` + `requirements.txt` + `main.py`, optionally layers requirements on top of a base venvpack (from `metadata.yaml`'s `basedependencies` URL), and uploads three files to fusion-index under an `app.{name}` artifact: the venvpack archive, `main.py`, and `metadata.yaml`
- `internal/gitutil/metadata.go`: in-memory git clone + YAML parse of `metadata.yaml`; extracts `name`, `version`, `builderImage`, `basedependencies`, and `runner`
- `api/v1alpha1`: `AppSourceSpec` struct and `"app"` added to `BuildType` enum; CRD YAML updated accordingly
- **Unified builder image config via K8s ConfigMap**: all build types (requirements, git, app) now look up their builder image by key from a dedicated `{release-name}-builder-images` ConfigMap, replacing the `BUILDER_IMAGE` / `BUILDER_IMAGE_PY310` env vars; keys for requirements/git builds remain the `python_version` strings (`"3.12"`, `"3.10"`); app builds use the `builderImage` key from `metadata.yaml`
- `deployment/values.yaml`: new top-level `builderImages` map replaces `server.config.builderImage` / `server.config.builderImagePy310`; new Helm template `configmap-builder-images.yaml` renders it as a K8s ConfigMap
- `deployment/templates/server-deployment.yaml`: `checksum/builder-images` pod annotation ensures the server pod restarts automatically when `builderImages` changes
- `deployment/templates/rbac.yaml`: server Role gains `configmaps: get` to allow reading the builder-images ConfigMap at startup
- DB migration `000006`: adds `runner` and `base_dependencies_url` columns to `venv_build`
- `runner` and `baseDependenciesUrl` fields in `VenvBuildResponse`

- Structured logging via `log/slog` throughout the server binary: JSON output by default, configurable via `LOG_FORMAT=json|text` and `LOG_LEVEL=debug|info|warn|error` env vars (both wired through Helm `server.config.logLevel` / `server.config.logFormat`)
- `internal/api/middleware/logging.go`: Gin middleware that generates a per-request ID, stamps a child `*slog.Logger` with `{request_id, method, path, client_ip}`, stores it in `gin.Context`, and logs the access line (status + latency) after each handler returns
- All 500-class errors now logged: `internalError()` logs via the per-request logger before writing the HTTP response; handler-level errors (artifact registry calls, CIBuild CR creation, status sync) log with structured fields including `build_id`, `name`, `version`
- Startup sequence (DB connect/ping, migrations, rules loading, K8s client setup) logged as structured events at INFO level

---

## [0.7.2] — 2026-05-18

### Fixed
- `builder/main.go` `uploadFile`: replaced full in-memory buffering (`bytes.Buffer` + `io.Copy`) with streaming via `io.MultiReader`; the multipart header is written to a small buffer to pre-compute `Content-Length`, then the file is streamed directly from disk — no allocation proportional to file size
- `internal/indexclient/client.go` `UploadFile`: same streaming fix; signature gains `size int64` so callers supply the file size and the method sets `Content-Length` without buffering

---

## [0.7.1] — 2026-05-18

### Added
- `linkerd.builderInject` Helm value — when set to `"disabled"`, adds `linkerd.io/inject: disabled` to builder Job pod annotations to prevent Job-completion hangs when the namespace has automatic Linkerd sidecar injection (the sidecar keeps running after the build exits and blocks Job completion); explicit `builderPodAnnotations` entries take precedence for the same key
- Note: large upload failures between builder and fusion-index are fixed by `linkerd.opaquePorts: "8080"` in the fusion-index chart (keeps mTLS), not by this value

---

## [0.7.0] — 2026-05-18

### Added
- Builder Job pods now get a pod-level `securityContext` with `runAsNonRoot`, `runAsUser`, and `seccompProfile` — defaults: `runAsNonRoot: true`, `runAsUser: 1000`, `seccompProfile.type: RuntimeDefault`
- `deployment/values.yaml`: `operator.config.builderPodSecurityContext` structured object to configure the builder pod security context per environment
- Operator env vars `BUILDER_POD_RUN_AS_NON_ROOT`, `BUILDER_POD_RUN_AS_USER`, `BUILDER_POD_SECCOMP_PROFILE` wired through `Config` → `CIBuildReconciler` → `BuildOptions` → `BuildJob`

### Fixed
- `builder/Dockerfile`, `builder/Dockerfile.py310`: added `useradd -u 1000` + `chown 1000:1000 /workspace` so the builder binary runs as UID 1000 and can write to `/workspace`; without this, the `runAsUser: 1000` pod security context caused `Permission denied` on first write

---

## [0.6.1] — 2026-05-11

### Fixed
- Helm chart: `BUILDER_JOB_LABELS`, `BUILDER_JOB_ANNOTATIONS`, `BUILDER_POD_LABELS`, `BUILDER_POD_ANNOTATIONS` were present in `k8s/deployment.yaml` (commented out) but absent from the Helm chart — operator container now receives them as env vars when set
- Helm chart: `BUILDER_IMAGE_PY310` was missing from the server ConfigMap (`server-configmap.yaml`) despite being supported since 0.6.0

### Added
- `deployment/values.yaml`: `operator.config.builderJobLabels/Annotations` and `builderPodLabels/Annotations` maps (default `{}`) — only rendered as env vars when non-empty
- `deployment/values.yaml`: `server.config.builderImagePy310` (default `fusion-venv-builder-py310:local`)
- `deployment/templates/_helpers.tpl`: `fusion-forge.mapToKeyValueCSV` helper — converts a Helm map to the `KEY=VALUE,...` format expected by `parseKeyValueCSV`

---

## [0.6.0] — 2026-05-08

### Added
- `python_version` field on `CreateVenvRequest` (multipart) and `CreateGitBuildRequest` (JSON) — accepted values: `"3.10"`, `"3.12"` (default)
- `BUILDER_IMAGE_PY310` env var: separate builder image for Python 3.10 builds
- `Config.BuilderImageForVersion(version)` helper — selects the right image; falls back to default (3.12) for unknown versions
- `builder/Dockerfile.py310` — builder image variant targeting Python 3.10
- Migration 000005: `python_version VARCHAR(10) NOT NULL DEFAULT '3.12'` column on `venv_build`

---

## [0.5.0] — 2026-05-07

### Added
- Deployment-time Job and Pod metadata injection via four new env vars: `BUILDER_JOB_LABELS`, `BUILDER_JOB_ANNOTATIONS`, `BUILDER_POD_LABELS`, `BUILDER_POD_ANNOTATIONS` — format: comma-separated `KEY=VALUE` pairs
- `BuildOptions` struct in `internal/jobbuilder/jobbuilder.go` — carries per-deployment metadata applied to every builder Job and Pod template
- `mergeWithSystemWin` helper — system-managed labels/annotations always take precedence over user-supplied values
- `parseKeyValueCSV` helper in `internal/config/config.go` — reusable parser for map-type env vars
- Operator now calls `config.Load()` at startup to pick up `BUILDER_*` fields

---

## [0.4.0] — 2026-04-18

### Added
- `metadata_source` field on git builds: `"manual"` (default), `"version"` (version from pyproject.toml), `"full"` (name + version from pyproject.toml)
- `internal/gitutil/pyproject.go` — in-memory go-git clone to parse `[project].name` / `[project].version` from `pyproject.toml` before the DB row is created
- `project_dir` field on git builds — optional relative path within a monorepo; shifts pyproject.toml lookup, structure validation, wheel build, and entrypoint resolution
- Migration 000003: `metadata_source VARCHAR(20) NOT NULL DEFAULT 'manual'`
- Migration 000004: `project_dir VARCHAR(500)` (nullable)
- Flux GitOps configuration: dev / staging / prod `HelmRelease` + `Kustomization` + cluster entry-points under `flux/`
- Image update automation via `ImageRepository`, `ImagePolicy`, `ImageUpdateAutomation`
- `ARCHITECTURE.md`, `EXAMPLES.md`, `FLUX.md`, `INSTALL.md` documentation

---

## [0.3.0] — 2026-04-17

### Added
- Git build endpoints: `POST /api/v1/gitbuilds`, `POST /api/v1/gitbuilds/validate`, `GET /api/v1/gitbuilds`, `GET /api/v1/gitbuilds/{id}`, `GET /api/v1/gitbuilds/{id}/logs`
- `GitSourceSpec` on `CIBuild` CRD — carries `repoURL`, `repoRef`, `entrypoint`, `buildType`, and `projectDir`
- `CreateGitBuildRequest` DTO (JSON body, not multipart)
- Migration 000002: `build_type`, `repo_url`, `repo_ref`, `entrypoint_file` columns on `venv_build`
- `internal/validation/forge-git-rules.yaml` + `GitRules` struct + `LoadGitRules` loader — embedded default git structure rules
- Builder extended: git clone → structure validation → `pip wheel` → venv install → tar.gz upload + optional entrypoint upload
- `internal/api/handlers/helpers.go` extracted: shared `pathID`, `internalError`, `syncStatusFromCR`, `podLogs` utilities

---

## [0.2.0] — 2026-04-16

### Added
- Complete rewrite from Java/Quarkus to Go 1.25 with Gin, pgx/v5, golang-migrate, and `sigs.k8s.io/controller-runtime`
- `CIBuild` CRD (`build.fusion-platform.io/v1alpha1`) — Kubernetes-native build orchestration
- CIBuild controller (`internal/controller/cibuild_controller.go`): creates ConfigMap + Job on new CR, updates phase to `Building → Succeeded / Failed`, deletes ConfigMap on terminal state
- `internal/jobbuilder/jobbuilder.go` — builds `batchv1.Job` + `corev1.ConfigMap` from a `CIBuild` spec
- REST API: `POST /api/v1/venvs`, `POST /api/v1/venvs/validate`, `GET /api/v1/venvs`, `GET /api/v1/venvs/{id}`, `GET /api/v1/venvs/{id}/logs`
- Lazy status sync on `GET /api/v1/venvs/{id}`: reads CIBuild CR and writes back to DB if phase changed
- K8s Service Account TokenReview auth (`internal/api/middleware/auth.go`) with `AUTH_ENABLED`, `AUTH_AUDIENCE`, `AUTH_ALLOWED_SA`
- `internal/indexclient/client.go` — typed HTTP client for fusion-index (`FindOrCreateArtifact`, `CreateVersion`, `UploadFile`)
- Requirements validation ported from Java: always-on rules (pip options, VCS/URL deps, PEP 508 name, version specifier required) + configurable rules via `forge-rules.yaml` (`require-exact-pinning`, `banned-packages`, `max-packages`)
- Migration 000001: `venv_build` table with `BIGSERIAL` primary key
- Helm chart under `deployment/` with full values for PostgreSQL, server, and operator
- `cmd/server/main.go` — wires DB pool, K8s client, index client, gin router
- `cmd/operator/main.go` — starts controller-runtime manager with CIBuild reconciler
- `builder/main.go` — statically linked Go binary (`CGO_ENABLED=0`): installs venv, creates tar.gz, uploads to fusion-index
- `Makefile` with `docker-build`, `docker-build-builder`, `minikube-deploy`, `generate` targets

### Removed
- Java/Quarkus implementation (Panache, Hibernate, Flyway, SmallRye, RESTEasy)

---

## [0.1.0] — 2026-04-03

### Added
- Initial Java 21 / Quarkus implementation of fusion-forge
- `POST /api/v1/venvs` — trigger a Python venv build via a Kubernetes Job
- Requirements validation: pip options, VCS deps, PEP 508 names, version specifier enforcement; configurable via `forge-rules.yaml`
- fusion-index HTTP client for artifact + version registration
- PostgreSQL persistence via Hibernate / Panache with Flyway migration `V1__create_venv_build.sql`
- Dev token filter for local development (`DevTokenFilter`)
- Kubernetes Job builder (`KubernetesJobService`) + build reconciler (`BuildReconciler`)
- `builder/build.sh` — shell-based venv builder script
- K8s manifests (`k8s/deployment.yaml`, `k8s/rbac.yaml`) for PostgreSQL, server, and builder
