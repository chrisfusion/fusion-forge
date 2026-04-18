# Installation

## Prerequisites

| Tool | Version |
|---|---|
| Go | 1.25+ |
| Docker | any recent |
| kubectl | 1.28+ |
| Helm | 3.14+ |
| minikube | 1.32+ (local dev only) |

fusion-forge expects a **fusion-index** instance to be reachable at `INDEX_BACKEND_URL` before any builds can complete.

---

## 1. Build images

### Inside minikube (local dev)

```bash
eval $(minikube docker-env)

# Server + operator image
make docker-build IMG=fusion-forge:local

# Builder image (Go binary + Python 3.12)
make docker-build-builder BUILDER_IMG=fusion-venv-builder:local
```

### For a registry (production)

```bash
make docker-build         IMG=registry.example.com/fusion/fusion-forge:1.0.0
make docker-build-builder BUILDER_IMG=registry.example.com/fusion/fusion-venv-builder:1.0.0

docker push registry.example.com/fusion/fusion-forge:1.0.0
docker push registry.example.com/fusion/fusion-venv-builder:1.0.0
```

---

## 2. Install the CRD

The Helm chart installs the `CIBuild` CRD from `deployment/crds/` automatically on `helm install`. To install it manually:

```bash
kubectl apply -f config/crd/bases/
```

---

## 3. Deploy with Helm

### Minimal (minikube)

```bash
helm upgrade --install fusion-forge ./deployment \
  --namespace fusion --create-namespace \
  --set server.image.repository=fusion-forge \
  --set server.image.tag=local \
  --set operator.image.repository=fusion-forge \
  --set operator.image.tag=local \
  --set server.config.builderImage=fusion-venv-builder:local
```

### Production with external registry

```bash
helm upgrade --install fusion-forge ./deployment \
  --namespace fusion --create-namespace \
  -f values-prod.yaml
```

Example `values-prod.yaml`:

```yaml
imagePullSecrets:
  - name: registry-credentials

server:
  image:
    repository: registry.example.com/fusion/fusion-forge
    tag: "1.0.0"
    pullPolicy: Always
  replicas: 2
  config:
    builderImage: registry.example.com/fusion/fusion-venv-builder:1.0.0
    indexBackendURL: http://fusion-index-backend.fusion.svc.cluster.local:8080
    authEnabled: "true"
    authAllowedSAs: "fusion/fusion-spectra"

operator:
  image:
    repository: registry.example.com/fusion/fusion-forge
    tag: "1.0.0"
    pullPolicy: Always

postgresql:
  auth:
    existingSecret: fusion-forge-db-secret   # key: "password"
  persistence:
    size: 10Gi
```

### External PostgreSQL

```yaml
postgresql:
  enabled: false
  external:
    host: postgres.example.com
    port: 5432
    database: fusion_forge
    username: fusion
    existingSecret: fusion-forge-db-secret   # key: "password"
```

---

## 4. Database migrations

Migrations run automatically at server startup via `golang-migrate`. No manual step is required on a fresh install.

To run migrations manually (e.g. for inspection or rollback):

```bash
# Install golang-migrate
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Apply all up migrations
migrate -path migrations/ \
  -database "postgres://fusion:fusion@localhost:5432/fusion_forge?sslmode=disable" up

# Roll back one step
migrate -path migrations/ \
  -database "postgres://fusion:fusion@localhost:5432/fusion_forge?sslmode=disable" down 1
```

---

## 5. Apply custom validation rules (optional)

Override the embedded defaults by mounting a ConfigMap and pointing the env vars at it:

```yaml
# forge-rules.yaml
require-exact-pinning: false
banned-packages:
  - pylint
  - black
max-packages: 50
```

```bash
kubectl create configmap forge-rules \
  --from-file=forge-rules.yaml -n fusion

# Add to server deployment env:
# FORGE_RULES_FILE=/rules/forge-rules.yaml
# Mount the ConfigMap at /rules/
```

---

## 6. Verify the installation

```bash
# Liveness
curl http://localhost:18080/q/health/live

# Readiness (checks DB connection)
curl http://localhost:18080/q/health/ready

# Validate a requirements build (no build started)
echo "numpy==1.26.4" > /tmp/req.txt
curl -s -X POST http://localhost:18080/api/v1/venvs/validate \
  -F name=test -F version=1.0.0 -F requirements=@/tmp/req.txt | jq .

# Validate a git build
curl -s -X POST http://localhost:18080/api/v1/gitbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{"name":"demo","version":"1.0.0","repo_url":"https://github.com/org/repo"}' | jq .
```

---

## 7. Authentication

When `AUTH_ENABLED=true`, every request to `/api/v1/**` must carry a Kubernetes ServiceAccount bearer token in the `Authorization: Bearer <token>` header.

```bash
# Obtain the token from within a pod
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)

curl -H "Authorization: Bearer $TOKEN" \
  http://fusion-forge.fusion.svc.cluster.local:8080/api/v1/venvs
```

The `AUTH_ALLOWED_SA` variable (comma-separated `namespace/name` pairs) restricts which service accounts may call the API. Leave it empty to accept any valid SA token.

---

## 8. Port-forward for local development

```bash
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

The service is then reachable at `http://localhost:18080`.
