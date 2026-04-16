# fusion-forge

Asynchronous Python virtual environment builder for the Fusion platform.

Accepts a `requirements.txt`, validates it, submits a Kubernetes `CIBuild` custom resource, and returns immediately. A co-located operator reconciles `CIBuild` objects by creating Kubernetes Jobs that run `pip install`, archive the resulting venv as a `.tar.gz`, and register the artifact in the [fusion-index](../fusion-index/) registry. Build status is tracked in PostgreSQL and lazily synced from the `CIBuild` CR on every `GET` request.

---

## Architecture

```
┌─────────────┐   POST /api/v1/venvs   ┌──────────────────┐
│   caller    │ ─────────────────────> │  fusion-forge     │
│ (SA token)  │ <─ 202 Accepted ─────  │  server (Go/Gin)  │
└─────────────┘                        └────────┬─────────┘
                                                │ create CIBuild CR
                                                ▼
                                       ┌──────────────────┐
                                       │  fusion-forge     │
                                       │  operator         │
                                       │  (controller-     │
                                       │   runtime)        │
                                       └────────┬─────────┘
                                                │ create Job + ConfigMap
                                                ▼
                                       ┌──────────────────┐
                                       │  builder pod      │
                                       │  (Go + Python 3.12│
                                       │   pip install     │
                                       │   → tar.gz        │
                                       │   → fusion-index) │
                                       └──────────────────┘
```

The server and operator run from the same Docker image (`fusion-forge`) but different entrypoints (`/server` vs `/operator`). Both are deployed as separate Kubernetes `Deployment` objects.

---

## Stack

| Layer | Technology |
|---|---|
| Language | Go 1.25 |
| REST API | Gin |
| Persistence | PostgreSQL 16 + pgx/v5 + golang-migrate |
| Operator | controller-runtime v0.19 |
| Auth | Kubernetes SA TokenReview |
| Builder pod | Go uploader binary + Python 3.12-slim |
| License | GPL-3.0 |

---

## API

Base path: `/api/v1/venvs`

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/venvs` | List builds — paginated, filterable, sortable |
| `POST` | `/api/v1/venvs` | Submit a venv build (multipart form) |
| `POST` | `/api/v1/venvs/validate` | Validate `requirements.txt` without starting a build |
| `GET` | `/api/v1/venvs/{id}` | Get build status and metadata (lazily syncs CR status) |
| `GET` | `/api/v1/venvs/{id}/logs` | Fetch builder pod logs |
| `GET` | `/q/health/live` | Liveness probe |
| `GET` | `/q/health/ready` | Readiness probe |

### Multipart form fields (`POST /api/v1/venvs` and `/validate`)

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Venv name (used as artifact name in fusion-index) |
| `version` | string | yes | Semver version, e.g. `1.0.0` |
| `description` | string | no | Free-text description |
| `requirements` | file | yes | `requirements.txt` content (max 100 KB) |

### List query parameters

| Parameter | Default | Description |
|---|---|---|
| `page` | `0` | Page index (0-based) |
| `pageSize` | `20` | Items per page (max 100) |
| `status` | — | Filter: `PENDING`, `BUILDING`, `SUCCESS`, `FAILED` |
| `name` | — | Substring match (case-insensitive) |
| `creatorId` | — | Exact match on creator SA username |
| `sortBy` | `createdAt` | `createdAt`, `updatedAt`, `name`, `version`, `status` |
| `sortDir` | `desc` | `asc` or `desc` |

### Build lifecycle

```
PENDING → BUILDING → SUCCESS
                   → FAILED
```

Status is stored in PostgreSQL. When a `GET /api/v1/venvs/{id}` request arrives for a `PENDING` or `BUILDING` build, the server reads the `CIBuild` CR and updates the DB row if the phase has changed. There is no background polling.

---

## Requirements validation

All builds are validated before any Kubernetes resource is created. Rules are loaded from `forge-rules.yaml` (embedded default; override with `FORGE_RULES_FILE`).

**Always enforced:**
- Valid PEP 508 package name
- Version specifier required (bare names like `pandas` are rejected)
- No pip options (`-r`, `-e`, `--index-url`, …)
- No VCS or URL dependencies (`git+https://…`, `http://…`)

**Configurable (`forge-rules.yaml`):**
```yaml
require-exact-pinning: true   # only == accepted; set false to allow >=, ~=, etc.
banned-packages: []           # case-insensitive; hyphens/underscores/dots normalised
max-packages: 100             # limit on valid requirement entries per file
```

`POST /api/v1/venvs/validate` returns `200` when valid, `422` with a violation list when not:

```json
{
  "valid": false,
  "violations": [
    { "line": 2, "content": "numpy>=1.0", "message": "exact version pin required — use ==" }
  ]
}
```

---

## Quick start (minikube)

```bash
# 1. Build images inside minikube
eval $(minikube docker-env)
make docker-build        IMG=fusion-forge:local
make docker-build-builder  BUILDER_IMG=fusion-venv-builder:local

# 2. Install CRD + RBAC + deploy
make deploy

# 3. Port-forward
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1

# 4. Smoke test
curl http://localhost:18080/q/health/ready
```

For the full Helm-based install see [install.md](install.md).
For an end-to-end API walkthrough see [example.md](example.md).

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `HTTP_PORT` | `8080` | REST server listen port |
| `DB_HOST` | `localhost` | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_NAME` | `fusion_forge` | Database name |
| `DB_USERNAME` | `fusion` | DB user |
| `DB_PASSWORD` | `fusion` | DB password |
| `DB_SSLMODE` | `disable` | `disable` or `require` |
| `K8S_NAMESPACE` | `fusion` | Namespace for `CIBuild` CRs and build Jobs |
| `INDEX_BACKEND_URL` | `http://fusion-index-backend.fusion.svc.cluster.local:8080` | fusion-index base URL |
| `BUILDER_IMAGE` | `ghcr.io/fusion-platform/venv-builder:latest` | Builder pod image |
| `AUTH_ENABLED` | `false` | `true` to require K8s SA bearer token on all `/api/v1/**` |
| `AUTH_AUDIENCE` | _(empty)_ | If set, token must have been issued for this audience |
| `AUTH_ALLOWED_SA` | _(empty)_ | Comma-separated `namespace/name` allowlist; empty = any valid SA |
| `FORGE_RULES_FILE` | _(empty)_ | Path to `forge-rules.yaml`; empty = use embedded default |

---

## Project structure

```
cmd/
  server/main.go           # REST server entry — DB + K8s clients + Gin
  operator/main.go         # Operator entry — controller-runtime manager
api/
  v1alpha1/
    cibuild_types.go       # CIBuild CRD spec/status structs
    zz_generated.deepcopy.go
config/
  crd/bases/               # Generated CRD YAML (controller-gen output)
internal/
  config/config.go         # Env-var loading
  db/db.go                 # pgx/v5 query functions
  validation/              # Requirements.txt validator + rule loader
  indexclient/client.go    # HTTP client for fusion-index
  api/
    middleware/auth.go     # K8s SA TokenReview auth
    dto/                   # Request/response types
    handlers/venvs.go      # REST handlers
    router.go              # Gin routes + CORS
  jobbuilder/jobbuilder.go # Builds K8s Job + ConfigMap from CIBuild spec
  controller/
    cibuild_controller.go  # CIBuild reconciler
builder/
  main.go                  # Builder binary: pip install → tar.gz → upload
  Dockerfile               # Multi-stage: Go build → python:3.12-slim-bookworm
deployment/                # Helm chart
  Chart.yaml
  values.yaml
  crds/                    # CIBuild CRD (installed by Helm before templates)
  templates/
k8s/                       # Raw manifests (minikube dev fallback)
migrations/                # golang-migrate SQL files
Dockerfile                 # Multi-stage: builds /server + /operator
Makefile
```
