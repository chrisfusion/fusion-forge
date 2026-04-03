# fusion-forge — Local Testing Guide

End-to-end walkthrough for deploying and testing fusion-forge on a local minikube cluster.

---

## Prerequisites

- `minikube` running (`minikube status`)
- `kubectl` configured to the minikube context (`kubectl config current-context` → `minikube`)
- `helm` 3.x installed
- `docker` available (the minikube Docker daemon will be used for builds)
- fusion-index source at `../fusion-index`
- fusion-forge source at `.` (this repo)

---

## 1. Deploy index-backend (fusion-index)

fusion-forge stores build artifacts in index-backend. Deploy it first.

```bash
# Point Docker at minikube's daemon (required before every new terminal session)
eval $(minikube docker-env)

# Build the index-backend image
docker build -t fusion-index:latest ../fusion-index/

# Install via Helm — namespace fusion must already exist
# values-dev.yaml sets: pullPolicy=Never, STORAGE_BACKEND=FILESYSTEM, postgres persistence disabled
helm upgrade --install fusion-index ../fusion-index/deployment/ \
  --namespace fusion \
  --create-namespace \
  -f ../fusion-index/deployment/values-dev.yaml \
  --wait --timeout 3m
```

> **Note:** `values-dev.yaml` has `createNamespace: false` because it assumes fusion-spectra created
> the namespace. Override with `--create-namespace` on first install as shown above.

Verify:

```bash
kubectl get pods -n fusion
# Expected: index-backend-* Running, fusion-index-postgresql-0 Running
```

### Fix: increase index-backend body size limit

The default Quarkus body size (10 MB) is too small for pandas-sized venv archives (~46 MB).
Apply this once after every fresh install:

```bash
kubectl set env deployment/index-backend -n fusion \
  QUARKUS_HTTP_LIMITS_MAX_BODY_SIZE=200M \
  QUARKUS_HTTP_LIMITS_MAX_FORM_ATTRIBUTE_SIZE=200M

kubectl rollout status deployment/index-backend -n fusion --timeout=60s
```

---

## 2. Build fusion-forge images

Both images must be built inside minikube's Docker daemon.

```bash
eval $(minikube docker-env)

# Main service image
docker build -f Dockerfile -t fusion-forge:local .

# Builder pod image (runs pip install inside Kubernetes)
docker build -f builder/Dockerfile -t fusion-venv-builder:local builder/
```

---

## 3. Deploy fusion-forge

### 3a. Apply RBAC

```bash
kubectl apply -f k8s/rbac.yaml
```

This creates:
- `fusion-forge` ServiceAccount (used by the Quarkus pod)
- `fusion-forge-role` + `fusion-forge-binding` (Jobs, Pods, ConfigMaps permissions)
- `fusion-forge-builder` ServiceAccount (used by builder pods, no K8s API permissions)

### 3b. Apply base deployment

```bash
kubectl apply -f k8s/deployment.yaml
```

This creates the Deployment, Service, PostgreSQL StatefulSet, and the DB secret.

### 3c. Patch for local dev

The base `deployment.yaml` is production-oriented (OIDC enabled, external image). Apply these
patches to make it work locally:

```bash
# Use the locally built image
kubectl set image deployment/fusion-forge forge=fusion-forge:local -n fusion
kubectl patch deployment fusion-forge -n fusion --type=json \
  -p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"}]'

# Dev configuration overrides
kubectl set env deployment/fusion-forge -n fusion \
  OIDC_ENABLED=false \
  FORGE_DEV_TOKEN=dev-token \
  BUILDER_IMAGE=fusion-venv-builder:local \
  FORGE_RULES_FILE=/etc/forge/rules.yaml

# Wait until ready
kubectl rollout status deployment/fusion-forge -n fusion --timeout=120s
```

> **Why `FORGE_RULES_FILE`?** The config property `forge.validation.rules-file` is defined in
> `application.properties` as `${FORGE_RULES_FILE:}` (empty default). Quarkus config injection
> fails when the resolved value is an empty string from an unset env var. Setting it to any
> non-empty path fixes the injection — the code falls back gracefully to the classpath
> `forge-rules.yaml` when the path doesn't exist.

### 3d. Verify startup

```bash
kubectl logs -n fusion deployment/fusion-forge --tail=15
```

Expected output (truncated):

```
INFO  [org.fly.cor.int.com.DbMigrate] Successfully applied 1 migration to schema "public"
INFO  [fus.for.bui.ser.TemplateInitService] Created venv-builder template id=1
WARN  [fus.for.val.RulesLoader] Rules file not found at /etc/forge/rules.yaml — using defaults
INFO  [fus.for.val.RulesLoader] Validation rules loaded — exactPinning=true bannedPackages=[] maxPackages=100
INFO  [io.quarkus] fusion-forge 0.1.0-SNAPSHOT on JVM started in 2.666s. Listening on: http://0.0.0.0:8080
```

The OIDC warning (`OIDC server is not available`) is harmless — OIDC is only checked on
authenticated requests. The dev token filter intercepts those first.

---

## 4. Port-forward

Open a dedicated terminal and keep it running:

```bash
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

Health check:

```bash
curl -s http://localhost:18080/q/health/ready | python3 -m json.tool
# Expected: {"status":"UP","checks":[{"name":"Database connections health check","status":"UP"}]}
```

Swagger UI is available at: http://localhost:18080/swagger-ui

---

## 5. API testing

All requests use the dev token set in step 3c. Replace `dev-token` if you chose a different value.

```bash
TOKEN=dev-token
BASE=http://localhost:18080
```

### 5a. Validate requirements.txt (dry run, no build)

```bash
cat > /tmp/requirements.txt << 'EOF'
pandas==2.2.2
numpy==1.26.4
python-dateutil==2.9.0
pytz==2024.1
six==1.16.0
EOF

curl -s -X POST $BASE/api/v1/venvs/validate \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=pandas-env" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/requirements.txt;type=text/plain" \
  | python3 -m json.tool
```

Expected (`200 OK`):

```json
{
    "valid": true,
    "violations": []
}
```

**Test an invalid file** (bare package name, rejected by exact-pinning rule):

```bash
printf 'pandas\nnumpy>=1.0\n' > /tmp/bad-requirements.txt

curl -s -X POST $BASE/api/v1/venvs/validate \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=test" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/bad-requirements.txt;type=text/plain" \
  | python3 -m json.tool
```

Expected (`422 Unprocessable Entity`):

```json
{
    "valid": false,
    "violations": [
        {"line": 1, "content": "pandas", "message": "version specifier is required ..."},
        {"line": 2, "content": "numpy>=1.0", "message": "exact version pin required — use == ..."}
    ]
}
```

### 5b. Submit a venv build

```bash
curl -s -X POST $BASE/api/v1/venvs \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=pandas-env" \
  -F "version=1.0.0" \
  -F "description=Simple pandas venv for testing" \
  -F "requirements=@/tmp/requirements.txt;type=text/plain" \
  | python3 -m json.tool
```

Expected (`202 Accepted`):

```json
{
    "id": 1,
    "name": "pandas-env",
    "version": "1.0.0",
    "status": "BUILDING",
    "k8sJobName": "forge-venv-1",
    "indexBackendJobId": 1,
    ...
}
```

Note the `id` — use it for status polling.

### 5c. Monitor build progress

**Poll status** (reconciler updates every 15 seconds):

```bash
curl -s $BASE/api/v1/venvs/1 -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

**Stream pod logs** (available once the builder pod starts):

```bash
curl -s $BASE/api/v1/venvs/1/logs -H "Authorization: Bearer $TOKEN"
```

**Watch the builder pod directly:**

```bash
kubectl logs -n fusion -l fusion.forge/build-id=1 -f
```

**Check K8s Job status:**

```bash
kubectl get jobs -n fusion -l app.kubernetes.io/managed-by=fusion-forge
```

A successful build produces logs ending with:

```
[forge-builder] pip install complete
[forge-builder] archive created: /workspace/pandas-env-1.0.0.tar.gz (46M)
[forge-builder] uploading to http://index-backend.fusion.svc.cluster.local:8080/api/v1/jobs/1/versions/1/artifacts
[forge-builder] artifact uploaded (HTTP 201)
[forge-builder] build complete
```

After the next reconciler tick (~15 s), `status` changes to `SUCCESS`.

### 5d. List builds

```bash
# All builds (newest first)
curl -s "$BASE/api/v1/venvs" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

# Filter by status
curl -s "$BASE/api/v1/venvs?status=SUCCESS" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

# Filter by name substring (case-insensitive)
curl -s "$BASE/api/v1/venvs?name=pandas" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

# Custom sort: oldest first
curl -s "$BASE/api/v1/venvs?sortBy=createdAt&sortDir=asc" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

# Paginate: page 1 (0-indexed), 5 items per page
curl -s "$BASE/api/v1/venvs?page=1&pageSize=5" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

### 5e. Conflict handling

Resubmitting the same `name:version` returns `409 Conflict`:

```bash
curl -s -X POST $BASE/api/v1/venvs \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=pandas-env" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/requirements.txt;type=text/plain" \
  | python3 -m json.tool
# {"code":409,"message":"A venv package 'pandas-env:1.0.0' already exists."}
```

---

## 6. Known issues and workarounds

| Issue | Root cause | Workaround |
|---|---|---|
| `Failed to load config value … forge.validation.rules-file` crash on startup | `${FORGE_RULES_FILE:}` empty default fails Quarkus config injection | Set `FORGE_RULES_FILE=/etc/forge/rules.yaml` (any non-empty string) |
| `413 Request Entity Too Large` on artifact upload | Quarkus default body limit (10 MB) too small for full venv archives | Set `QUARKUS_HTTP_LIMITS_MAX_BODY_SIZE=200M` on index-backend |
| `OIDC server is not available` warning in logs | Quarkus still probes auth server even when `OIDC_ENABLED=false` | Harmless warning — dev token filter handles auth before OIDC is checked |

---

## 7. Teardown

```bash
# Remove forge resources
kubectl delete -f k8s/deployment.yaml
kubectl delete -f k8s/rbac.yaml

# Remove index-backend
helm uninstall fusion-index -n fusion

# Remove namespace (deletes everything)
kubectl delete namespace fusion
```
