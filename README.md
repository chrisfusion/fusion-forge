# fusion-forge

Asynchronous Python build service for the fusion platform.

fusion-forge accepts a `requirements.txt`, a Git repository (library or Streamlit-style app), and registers the result — a Python virtual environment archive — in the [fusion-index](../fusion-index/) artifact registry. Builds run as Kubernetes Jobs orchestrated by a co-located operator, and a GitOps watcher can trigger new builds automatically when a repository changes.

---

## Features

- **Requirements builds** (`/api/v1/venvs`) — upload a `requirements.txt`; forge validates, builds, and registers a pinned venv
- **Git builds** (`/api/v1/gitbuilds`) — point at any Git repository; forge clones, builds a wheel from `pyproject.toml`, and installs it
- **App builds** (`/api/v1/appbuilds`) — point at a repository containing `metadata.yaml` + `requirements.txt` + `main.py`; forge builds a venv, optionally layered on a base venvpack, and uploads the venv archive, entrypoint, and metadata together
- **GitOps watcher** — register a `GitWatcher` CR (or via `/api/v1/gitwatchers`) to poll a repository and auto-trigger a git or app build whenever a new version is published
- **Monorepo support** — optional `project_dir` field targets a subdirectory within a repository
- **Metadata auto-detection** — server-side `pyproject.toml` parsing extracts `name` and/or `version` before a git build starts (`metadata_source`: `manual` / `version` / `full`)
- **Python version selection** — `python_version` chooses the builder image for requirements and git builds (`3.10` / `3.12`, default `3.12`)
- **Async lifecycle** — builds run as Kubernetes Jobs managed by a co-located operator; status is lazily synced on `GET`
- **Bulk maintenance** — `DELETE /api/v1/builds` removes old builds by status/age; `POST /api/v1/builds/zombie-cleanup` reconciles DB rows whose CIBuild CR was deleted out-of-band
- **Validation endpoints** — pre-flight check any request without starting a build or touching the registry
- **Private repositories** — `tokenSecretRef` authenticates both the server's metadata pre-fetch and the builder pod's git clone against a K8s Secret
- **K8s SA token auth** — optional `TokenReview`-based authentication
- **Structured logging** — JSON or text `log/slog` output, per-request fields

---

## API overview

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/venvs` | List requirements builds |
| `POST` | `/api/v1/venvs` | Submit a requirements build (multipart) |
| `POST` | `/api/v1/venvs/validate` | Validate without building |
| `GET` | `/api/v1/venvs/:id` | Get status (lazily syncs CR) |
| `GET` | `/api/v1/venvs/:id/logs` | Fetch builder pod logs |
| `GET` | `/api/v1/gitbuilds` | List git builds |
| `POST` | `/api/v1/gitbuilds` | Submit a git build (JSON) |
| `POST` | `/api/v1/gitbuilds/validate` | Validate without building |
| `GET` | `/api/v1/gitbuilds/:id` | Get status (lazily syncs CR) |
| `GET` | `/api/v1/gitbuilds/:id/logs` | Fetch builder pod logs |
| `GET` | `/api/v1/appbuilds` | List app builds |
| `POST` | `/api/v1/appbuilds` | Submit an app build (JSON; metadata read from `metadata.yaml`) |
| `POST` | `/api/v1/appbuilds/validate` | Validate without building |
| `GET` | `/api/v1/appbuilds/:id` | Get status (lazily syncs CR) |
| `GET` | `/api/v1/appbuilds/:id/logs` | Fetch builder pod logs |
| `GET` | `/api/v1/gitwatchers` | List GitWatcher CRs |
| `POST` | `/api/v1/gitwatchers` | Create a GitWatcher CR (verifies repo reachability) |
| `GET` | `/api/v1/gitwatchers/:name` | Get a GitWatcher CR |
| `PUT` | `/api/v1/gitwatchers/:name` | Replace a GitWatcher CR's spec |
| `DELETE` | `/api/v1/gitwatchers/:name` | Delete a GitWatcher CR |
| `DELETE` | `/api/v1/builds` | Bulk-delete builds by status + age |
| `POST` | `/api/v1/builds/zombie-cleanup` | Reconcile builds whose CIBuild CR no longer exists |
| `GET` | `/q/health/live` | Liveness probe |
| `GET` | `/q/health/ready` | Readiness probe (checks DB) |

Requirements-build endpoints use `multipart/form-data`; every other write endpoint takes a JSON body.

---

## Quick start (minikube)

```bash
# Build images inside minikube
eval $(minikube docker-env)
make docker-build                 IMG=fusion-forge:local
make docker-build-builder         BUILDER_IMG=fusion-venv-builder:local
make docker-build-builder-py310   BUILDER_IMG_PY310=fusion-venv-builder-py310:local

# Install the GitWatcher CRD (not Helm-managed, to avoid field-manager conflicts)
kubectl apply -f config/crd/bases/build.fusion-platform.io_gitwatchers.yaml

# Deploy (Helm is the primary deployment method — see below)
helm upgrade --install fusion-forge deployment/ -n fusion

# Port-forward (keep running in a separate terminal)
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1

# Verify
curl http://localhost:18080/q/health/ready

# Submit a requirements build
curl -X POST http://localhost:18080/api/v1/venvs \
  -F name=mypackage -F version=1.0.0 -F requirements=@requirements.txt

# Submit a git build (name + version from pyproject.toml)
curl -X POST http://localhost:18080/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{"repo_url":"https://github.com/org/repo","metadata_source":"full"}'

# Submit an app build (name/version/builderImage all come from metadata.yaml)
curl -X POST http://localhost:18080/api/v1/appbuilds \
  -H "Content-Type: application/json" \
  -d '{"repo_url":"https://github.com/org/streamlit-app"}'
```

See [EXAMPLES.md](EXAMPLES.md) for the full set of curl examples (validation, listing/filtering, GitWatcher CRUD, bulk maintenance, error cases).

---

## Deploying with Helm

Helm is the primary and only supported deployment method (`k8s/deployment.yaml` and the `make deploy`/`make undeploy` raw-manifest targets exist for quick local iteration only).

### Install

```bash
# CRDs first — CIBuild is Helm-managed via deployment/crds/, but GitWatcher is not
kubectl apply -f config/crd/bases/build.fusion-platform.io_gitwatchers.yaml

# minikube (local images)
helm upgrade --install fusion-forge deployment/ \
  --namespace fusion \
  --set server.image.tag=local \
  --set operator.image.tag=local \
  --set watcher.image.tag=local \
  --wait --timeout 3m

# production — override at minimum image tags, postgresql auth, and builderImages
helm upgrade --install fusion-forge deployment/ \
  --namespace fusion \
  --values my-values.yaml \
  --wait --timeout 5m
```

If a `CIBuild` CRD from a previous `kubectl apply` conflicts with Helm's field manager on first install:

```bash
kubectl delete crd cibuilds.build.fusion-platform.io
helm install fusion-forge deployment/ -n fusion
```

### Key values (`deployment/values.yaml`)

| Key | Purpose |
|---|---|
| `builderImages` | Map of builder-image key → image ref (python_version strings for venv/git builds, `metadata.yaml`'s `builderImage` key for app builds) |
| `server.config.authEnabled` / `authAudience` / `authAllowedSAs` | K8s SA TokenReview auth |
| `server.config.logLevel` / `logFormat` | `debug\|info\|warn\|error`, `json\|text` |
| `operator.config.builderJobLabels/Annotations`, `builderPodLabels/Annotations` | Deployment-time metadata injected into every builder Job/Pod |
| `operator.config.builderPodSecurityContext` | Pod-level security context for builder Jobs |
| `watcher.config.pollInterval` / `maxFailures` | GitWatcher polling cadence and auto-disable threshold |
| `linkerd.builderInject` | Set `"disabled"` to prevent Linkerd sidecar from blocking builder Job completion |
| `postgresql.enabled` / `postgresql.external.*` | Bundled StatefulSet vs. external PostgreSQL |
| `ingress.*` | Optional ingress for the REST API |

### Upgrade

```bash
helm upgrade fusion-forge deployment/ \
  --namespace fusion \
  --set server.image.tag=0.11.0 \
  --set operator.image.tag=0.11.0 \
  --set watcher.image.tag=0.11.0 \
  --wait
```

Schema migrations run automatically on server startup via `golang-migrate` and are idempotent.

### Uninstall

```bash
helm uninstall fusion-forge -n fusion

# PVC and CRDs are not removed automatically
kubectl delete pvc -n fusion -l app.kubernetes.io/name=fusion-forge   # destroys build history
kubectl delete -f config/crd/bases/                                   # removes all CIBuild/GitWatcher objects cluster-wide
```

---

## Stack

| Layer | Technology |
|---|---|
| Language | Go 1.25 |
| REST API | Gin |
| Persistence | PostgreSQL 16 · pgx/v5 · golang-migrate |
| Operator / Watcher | controller-runtime v0.19 |
| Auth | Kubernetes SA TokenReview |
| Logging | `log/slog` (server), `logr`/`zap` (operator, watcher) |
| Builder pod | Go binary + Python 3.10/3.12-slim |
| Deployment | Helm 3 (self-contained, no external subcharts) |
| License | GPL-3.0 |

---

## Documentation

| Document | Contents |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Component overview, data flow, CRD lifecycle, DB schema |
| [EXAMPLES.md](EXAMPLES.md) | Full curl walkthrough and reference for every endpoint |
| [FLUX.md](FLUX.md) | Flux GitOps deployment guide (dev/staging/prod) |
| [CHANGELOG.md](CHANGELOG.md) | Release history |
