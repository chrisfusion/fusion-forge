# Changelog

All notable changes to fusion-forge are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

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
