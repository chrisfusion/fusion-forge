# fusion-forge

REST service that builds Python 3.12 virtual environments asynchronously via Kubernetes Jobs and stores the resulting tar.gz in the fusion index-backend registry.

## Stack

- **Java 21**, **Maven 3.9**, **Quarkus 3.17.7**
- **PostgreSQL** via `quarkus-hibernate-orm-panache` + `quarkus-flyway`
- **Kubernetes** via `quarkus-kubernetes-client` (Fabric8, in-cluster)
- **OIDC** via `quarkus-oidc`; dev/test override: set `FORGE_DEV_TOKEN` env var — a `ContainerRequestFilter` short-circuits OIDC when the Bearer token matches
- **License**: GPL-3.0

## Commands

```bash
# Dev mode (requires FORGE_DEV_TOKEN set, index-backend port-forwarded)
mvn quarkus:dev

# Build
mvn package -DskipTests

# Build verification (Docker)
docker build -f Dockerfile -t fusion-forge:local .

# Build the venv builder image
docker build -f builder/Dockerfile -t fusion-venv-builder:local builder/

# Run tests
mvn test

# Local minikube dev deployment (eval must be re-run in every new terminal before docker build)
eval $(minikube docker-env)
kubectl apply -f k8s/rbac.yaml
kubectl apply -f k8s/deployment.yaml
kubectl set image deployment/fusion-forge forge=fusion-forge:local -n fusion
kubectl patch deployment fusion-forge -n fusion --type=json \
  -p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"}]'
kubectl set env deployment/fusion-forge -n fusion \
  OIDC_ENABLED=false FORGE_DEV_TOKEN=dev-token \
  BUILDER_IMAGE=fusion-venv-builder:local FORGE_RULES_FILE=/etc/forge/rules.yaml

# Port-forward (keep running in a separate terminal)
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

## Project Structure

```
src/main/java/fusion/forge/
  api/
    VenvResource.java          # GET /, POST /, POST /validate, GET /{id}, GET /{id}/logs
    dto/                       # CreateVenvRequest, VenvBuildResponse, VenvBuildPageResponse, ValidationResponse
    ExceptionMappers.java
    auth/DevTokenFilter.java   # OIDC dev override
  build/
    entity/VenvBuild.java      # PanacheEntity — build state machine
    enums/BuildStatus.java     # PENDING, BUILDING, SUCCESS, FAILED
    service/VenvBuildService.java
    k8s/
      KubernetesJobService.java  # create/delete K8s Job + ConfigMap
      BuildReconciler.java       # @Scheduled every 15s — syncs K8s → DB
  validation/
    Violation.java              # record: line, content, message
    ValidationResult.java       # record: valid, List<Violation>
    RequirementsRules.java      # POJO: requireExactPinning, bannedPackages, maxPackages
    RulesLoader.java            # @ApplicationScoped — loads forge-rules.yaml at startup
    RequirementsValidator.java  # @ApplicationScoped — validates line by line
  client/
    IndexBackendClient.java    # @RegisterRestClient for index-backend
src/main/resources/
  application.properties
  forge-rules.yaml             # operator-configurable validation rules (mountable as K8s ConfigMap)
  db/migration/                # Flyway scripts V1__...sql
builder/
  Dockerfile                   # python:3.12-slim-bookworm based
  build.sh                     # creates venv, tar.gz, uploads to index-backend
k8s/
  rbac.yaml                    # ServiceAccount, Role, RoleBinding
  deployment.yaml
```

## Platform Context

- **Namespace**: `fusion` (minikube)
- **Reference implementation**: `fusion-index` (sibling project) — follow its patterns for Panache entities, Flyway migrations, resource/service/mapper structure, Helm chart layout
- **index-backend**: artifact registry at `http://index-backend.fusion.svc.cluster.local:8080`; OpenAPI at `/openapi`; key endpoints: `POST /api/v1/jobs`, `POST /api/v1/jobs/{id}/versions/{n}/artifacts`
- **Build jobs**: one Kubernetes Job per venv build; requirements.txt passed via ConfigMap volume at `/workspace/requirements.txt`; builder pod calls index-backend directly to upload artifact
- **K8s RBAC**: `fusion-forge` ServiceAccount needs Jobs+Pods+ConfigMaps create/get/list/watch in `fusion` namespace; builder pods use a separate SA with no K8s API permissions

## Key Config Properties

| Property | Description |
|---|---|
| `forge.k8s.namespace` | Namespace for build Jobs (default: `fusion`) |
| `forge.builder.image` | Builder pod image |
| `forge.dev-token` | Dev OIDC override token (via `FORGE_DEV_TOKEN` env var) |
| `quarkus.rest-client.index-backend.url` | index-backend base URL (used by REST client) |
| `forge.index-backend.url` | Same URL injected into builder pod env — set via `${quarkus.rest-client.index-backend.url}` interpolation; keep in sync |
| `quarkus.oidc.auth-server-url` | OIDC issuer |
| `forge.validation.rules-file` | Path to validation rules YAML (default: classpath `forge-rules.yaml`; override with `FORGE_RULES_FILE` env var for K8s ConfigMap mount) |

## Gotchas

- **Entity convention**: always extend `PanacheEntity` (BIGINT + sequence), never `PanacheEntityBase` + UUID — matches fusion-index platform pattern; REST IDs are `Long`
- **Transaction pattern in `VenvBuildService`**: external HTTP/K8s calls run *outside* `@Transactional`; only small DB-only methods are `@Transactional` (`createPending`, `markBuilding`, `markFailed`) — prevents partial-failure rollbacks from masking orphaned index-backend/K8s resources
- `CreateJobRequest` in index-backend requires `gitUrl` and `gitRef` (mandatory fields even for non-git artifacts) — use `"venv://forge"` and the version string as synthetic values
- K8s Job `backoffLimit: 0` — pip failures fail fast with no retry; client must resubmit
- ConfigMap is created before the K8s Job and must be deleted after the Job completes (handled by `BuildReconciler`)
- `FORGE_DEV_TOKEN` must never be set in production deployments; add a startup `WARN` log if it is set outside dev/test profile
- **`FORGE_RULES_FILE` must be non-empty in K8s**: `${FORGE_RULES_FILE:}` empty default in `application.properties` crashes Quarkus config injection at startup (`Failed to load config value … forge.validation.rules-file`). Set to any non-empty path (e.g. `/etc/forge/rules.yaml`); `RulesLoader` falls back to classpath default when the file doesn't exist. Fix target: change to a non-empty default or `Optional<String>`.
- **index-backend body size**: default Quarkus limit (10 MB) rejects venv archives (~46 MB). Set `QUARKUS_HTTP_LIMITS_MAX_BODY_SIZE=200M` and `QUARKUS_HTTP_LIMITS_MAX_FORM_ATTRIBUTE_SIZE=200M` on the index-backend deployment.
- **Failed builds block same `name:version` resubmission** — `createPending` enforces uniqueness regardless of build status. Use a new version string after a failure.

## Validation

`RequirementsValidator` validates requirements.txt before any build is started. Rules are loaded from `forge-rules.yaml` (classpath default, override with `FORGE_RULES_FILE`).

**Always-on rules (not configurable):**
- Each line must be valid pip syntax with a plausible PEP 508 package name
- A version specifier must be present — bare package names are rejected
- Pip options rejected: `-r`, `--index-url`, `-e`, etc. produce a violation
- VCS/URL dependencies rejected: `git+https://...`, `http://...`

**Configurable rules (`forge-rules.yaml`):**
```yaml
require-exact-pinning: true   # true = only == accepted; false = any specifier (>=, ~=, etc.)
banned-packages: []           # case-insensitive; hyphens/underscores/dots normalised
max-packages: 100             # counts only fully valid entries; invalid lines excluded
```

**API:**
- `POST /api/v1/venvs` — `400 Bad Request` with violations JSON if invalid; proceeds to build if valid
- `POST /api/v1/venvs/validate` — `200 OK` when valid; `422 Unprocessable Entity` when invalid; body always `{ "valid": bool, "violations": [{line, content, message}] }`
- `jackson-dataformat-yaml` (Quarkus BOM) used to parse `forge-rules.yaml` in `RulesLoader`

**Validation gotchas:**
- `RulesLoader.rules` is initialized to `new RequirementsRules()` (safe built-in defaults) — `getRules()` is never null even before `StartupEvent` fires
- `packageCount` only increments for fully valid lines — banned and version-invalid entries are excluded from the max-packages count
- `ValidationResponse` and `Violation` are Java `record` types; access components via method syntax (e.g. `response.valid()`, not `.valid`)
