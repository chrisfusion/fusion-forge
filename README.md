# fusion-forge

Asynchronous Python virtual environment builder for the Fusion platform.

Accepts a `requirements.txt`, validates it, spawns a Kubernetes Job that runs `pip install`, archives the resulting venv as a `.tar.gz`, and registers the artifact in the [fusion-index](../fusion-index/) registry. Build status is tracked in PostgreSQL and reconciled every 15 seconds from Kubernetes Job state.

---

## Features

- **Validation-first** — requirements.txt is checked before any build starts; rejects bare names, VCS/URL deps, pip options, and configurable rules (exact pinning, banned packages, package count limit)
- **Async build pipeline** — one Kubernetes Job per venv; no long-running HTTP connections
- **Artifact registry integration** — built venvs are stored in fusion-index and retrievable via its API
- **Paginated build list** — query, filter, and sort all builds
- **Dev-friendly** — OIDC can be bypassed with a static dev token; Swagger UI always available

---

## Stack

| Layer | Technology |
|---|---|
| Runtime | Java 21, Quarkus 3.17.7 |
| Build | Maven 3.9 |
| Persistence | PostgreSQL 16 + Hibernate ORM Panache + Flyway |
| Kubernetes | Fabric8 client (in-cluster) |
| Auth | Quarkus OIDC (Keycloak) |
| Builder pod | Python 3.12-slim + pip |
| License | GPL-3.0 |

---

## API

Base path: `/api/v1/venvs`

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/venvs` | List builds — paginated, filterable, sortable |
| `POST` | `/api/v1/venvs` | Submit a venv build (multipart: name, version, description, requirements file) |
| `POST` | `/api/v1/venvs/validate` | Validate requirements.txt without starting a build |
| `GET` | `/api/v1/venvs/{id}` | Get build status and metadata |
| `GET` | `/api/v1/venvs/{id}/logs` | Stream builder pod logs |
| `GET` | `/openapi` | OpenAPI spec (YAML; add `?format=json` for JSON) |
| `GET` | `/swagger-ui` | Interactive API docs |
| `GET` | `/q/health/ready` | Readiness probe |
| `GET` | `/q/health/live` | Liveness probe |

### List query parameters

| Parameter | Default | Description |
|---|---|---|
| `page` | `0` | Page index (0-based) |
| `pageSize` | `20` | Items per page (max 100) |
| `status` | — | Filter: `PENDING`, `BUILDING`, `SUCCESS`, `FAILED` |
| `name` | — | Substring match (case-insensitive) |
| `creatorId` | — | Exact match on creator user ID |
| `sortBy` | `createdAt` | `createdAt`, `updatedAt`, `name`, `version`, `status` |
| `sortDir` | `desc` | `asc` or `desc` |

### Build lifecycle

```
PENDING → BUILDING → SUCCESS
                   → FAILED
```

Reconciler runs every 15 seconds and syncs Kubernetes Job status into the database. Failed builds do **not** retry automatically — submit a new request with a different version string.

---

## Requirements validation

All builds are validated before the Kubernetes Job is created. Validation rules are loaded from `forge-rules.yaml` (classpath default; mount a ConfigMap and set `FORGE_RULES_FILE` to override).

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
    { "line": 3, "content": "requests>=2.0", "message": "exact version pin required — use ==" }
  ]
}
```

---

## Quick start (local minikube)

### Prerequisites

- minikube running, `fusion` namespace exists
- fusion-index deployed and index-backend reachable at `http://index-backend.fusion.svc.cluster.local:8080`
- `eval $(minikube docker-env)` run in the current terminal

### Build images

```bash
eval $(minikube docker-env)
docker build -f Dockerfile -t fusion-forge:local .
docker build -f builder/Dockerfile -t fusion-venv-builder:local builder/
```

### Deploy

```bash
kubectl apply -f k8s/rbac.yaml
kubectl apply -f k8s/deployment.yaml

# Patch for local dev (OIDC off, local images, dev token)
kubectl set image deployment/fusion-forge forge=fusion-forge:local -n fusion
kubectl patch deployment fusion-forge -n fusion --type=json \
  -p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"}]'
kubectl set env deployment/fusion-forge -n fusion \
  OIDC_ENABLED=false \
  FORGE_DEV_TOKEN=dev-token \
  BUILDER_IMAGE=fusion-venv-builder:local \
  FORGE_RULES_FILE=/etc/forge/rules.yaml

kubectl rollout status deployment/fusion-forge -n fusion --timeout=120s
```

### Port-forward

```bash
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

### Test

```bash
# Health check
curl http://localhost:18080/q/health/ready

# Validate a requirements file
curl -X POST http://localhost:18080/api/v1/venvs/validate \
  -H "Authorization: Bearer dev-token" \
  -F "name=myenv" -F "version=1.0.0" \
  -F "requirements=@/path/to/requirements.txt;type=text/plain"

# Submit a build
curl -X POST http://localhost:18080/api/v1/venvs \
  -H "Authorization: Bearer dev-token" \
  -F "name=myenv" -F "version=1.0.0" \
  -F "description=My environment" \
  -F "requirements=@/path/to/requirements.txt;type=text/plain"

# Poll status (use the id from the response above)
curl http://localhost:18080/api/v1/venvs/1 -H "Authorization: Bearer dev-token"

# Stream builder logs
curl http://localhost:18080/api/v1/venvs/1/logs -H "Authorization: Bearer dev-token"

# List all builds
curl "http://localhost:18080/api/v1/venvs?status=SUCCESS&sortBy=createdAt&sortDir=desc" \
  -H "Authorization: Bearer dev-token"
```

See [testing.md](testing.md) for the full end-to-end walkthrough.

---

## Configuration

| Environment variable | Property | Default | Description |
|---|---|---|---|
| `DB_HOST` | `quarkus.datasource.jdbc.url` | `localhost` | PostgreSQL host |
| `DB_PORT` | — | `5432` | PostgreSQL port |
| `DB_NAME` | — | `fusion_forge` | Database name |
| `DB_USERNAME` / `DB_PASSWORD` | — | `fusion` / `fusion` | Database credentials |
| `INDEX_BACKEND_HOST` | `quarkus.rest-client.index-backend.url` | `index-backend.fusion.svc.cluster.local` | index-backend host |
| `INDEX_BACKEND_PORT` | — | `8080` | index-backend port |
| `K8S_NAMESPACE` | `forge.k8s.namespace` | `fusion` | Namespace for build Jobs |
| `BUILDER_IMAGE` | `forge.builder.image` | `ghcr.io/fusion-platform/venv-builder:latest` | Builder pod image |
| `FORGE_RULES_FILE` | `forge.validation.rules-file` | _(classpath default)_ | Path to `forge-rules.yaml`; must be non-empty in K8s |
| `FORGE_DEV_TOKEN` | `forge.dev-token` | _(disabled)_ | Dev OIDC bypass token — **never set in production** |
| `OIDC_ENABLED` | `quarkus.oidc.enabled` | `true` | Set `false` to disable OIDC |
| `OIDC_AUTH_SERVER_URL` | `quarkus.oidc.auth-server-url` | Keycloak in `fusion` namespace | OIDC issuer URL |

---

## Project structure

```
src/main/java/fusion/forge/
  api/
    VenvResource.java           # REST endpoints
    dto/                        # Request/response DTOs
    mapper/VenvBuildMapper.java
    auth/DevTokenFilter.java    # Dev token OIDC bypass
    ExceptionMappers.java
  build/
    entity/VenvBuild.java       # Panache entity — build state machine
    enums/BuildStatus.java      # PENDING, BUILDING, SUCCESS, FAILED
    service/
      VenvBuildService.java     # Orchestrates build initiation
      TemplateInitService.java  # Ensures venv-builder template in index-backend at startup
    k8s/
      KubernetesJobService.java # Creates/queries K8s Jobs and ConfigMaps
      BuildReconciler.java      # @Scheduled every 15s — syncs K8s → DB
  validation/
    RequirementsValidator.java  # Line-by-line PEP 508 + rule validation
    RulesLoader.java            # Loads forge-rules.yaml at startup
    RequirementsRules.java      # Rule configuration POJO
  client/
    IndexBackendClient.java     # MicroProfile REST client for index-backend
src/main/resources/
  application.properties
  application-dev.properties
  forge-rules.yaml              # Default validation rules (mountable as ConfigMap)
  db/migration/                 # Flyway migrations
builder/
  Dockerfile                    # python:3.12-slim-bookworm
  build.sh                      # pip install → tar.gz → upload to index-backend
k8s/
  rbac.yaml                     # ServiceAccounts, Role, RoleBinding
  deployment.yaml               # Deployment, Service, PostgreSQL StatefulSet
testing.md                      # Full end-to-end testing guide
```
