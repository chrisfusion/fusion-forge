# fusion-forge — Usage Examples

End-to-end walkthrough for the fusion-forge REST API. Assumes the server is port-forwarded to `localhost:18080` (see [install.md](install.md)).

```bash
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

Set a base URL for all examples:

```bash
BASE=http://localhost:18080
```

Auth is disabled by default (`AUTH_ENABLED=false`). If your deployment has auth enabled, add `-H "Authorization: Bearer <sa-token>"` to every request.

---

## 1. Validate a requirements.txt (dry run)

Validation runs without creating any Kubernetes resources or database rows.

```bash
cat > /tmp/requirements.txt << 'EOF'
requests==2.31.0
numpy==1.26.4
pandas==2.2.2
EOF

curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=data-env" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/requirements.txt" \
  | python3 -m json.tool
```

Expected (`200 OK`):

```json
{
    "valid": true,
    "violations": []
}
```

### Validation failure examples

**Bare package name (version specifier required):**

```bash
printf 'pandas\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" \
  | python3 -m json.tool
```

```json
{
    "valid": false,
    "violations": [
        {
            "line": 1,
            "content": "pandas",
            "message": "version specifier is required (e.g. pandas==2.2.2)"
        }
    ]
}
```

**Inexact pin (when `require-exact-pinning: true`):**

```bash
printf 'numpy>=1.0\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" \
  | python3 -m json.tool
```

```json
{
    "valid": false,
    "violations": [
        {
            "line": 1,
            "content": "numpy>=1.0",
            "message": "exact version pin required — use =="
        }
    ]
}
```

**VCS dependency:**

```bash
printf 'git+https://github.com/org/repo.git\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" \
  | python3 -m json.tool
```

```json
{
    "valid": false,
    "violations": [
        {
            "line": 1,
            "content": "git+https://github.com/org/repo.git",
            "message": "VCS and URL dependencies are not allowed"
        }
    ]
}
```

---

## 2. Submit a build

```bash
cat > /tmp/requirements.txt << 'EOF'
requests==2.31.0
numpy==1.26.4
EOF

curl -s -X POST $BASE/api/v1/venvs \
  -F "name=data-env" \
  -F "version=1.0.0" \
  -F "description=Minimal data science environment" \
  -F "requirements=@/tmp/requirements.txt" \
  | python3 -m json.tool
```

Expected (`202 Accepted`):

```json
{
    "id": 1,
    "name": "data-env",
    "version": "1.0.0",
    "description": "Minimal data science environment",
    "status": "PENDING",
    "ciBuildName": "forge-venv-1",
    "indexArtifactId": 42,
    "indexArtifactVersion": "1.0.0",
    "createdAt": "2026-04-16T21:17:34Z",
    "updatedAt": "2026-04-16T21:17:34Z"
}
```

Note the `id` — use it to poll status and fetch logs.

---

## 3. Watch the build

### Check the CIBuild CR

The operator creates a `CIBuild` CR and drives a Kubernetes Job from it:

```bash
kubectl get cibuild forge-venv-1 -n fusion
```

```
NAME           ARTIFACT   VERSION   PHASE      AGE
forge-venv-1   data-env   1.0.0     Building   12s
```

```bash
kubectl get cibuild forge-venv-1 -n fusion -o yaml
```

Shows the full spec (including `configData.requirements.txt`) and status (`jobName`, `phase`, `startedAt`).

### Watch the builder pod

```bash
# The Job spawns a pod; follow its logs
kubectl logs -n fusion -l job-name=forge-job-forge-venv-1 -f
```

Expected output:

```
2026/04/16 21:17:35 [forge-builder] starting: artifact=data-env version=1.0.0
2026/04/16 21:17:35 [forge-builder] creating virtual environment
2026/04/16 21:17:36 [forge-builder] upgrading pip
2026/04/16 21:17:37 [forge-builder] installing packages
Collecting requests==2.31.0 ...
Collecting numpy==1.26.4 ...
Successfully installed ...
2026/04/16 21:17:42 [forge-builder] creating archive data-env-1.0.0.tar.gz
2026/04/16 21:17:43 [forge-builder] archive size: 25985912 bytes
2026/04/16 21:17:43 [forge-builder] uploading to http://fusion-index-backend.fusion.svc.cluster.local:8080/...
2026/04/16 21:17:43 [forge-builder] uploaded file id=42
2026/04/16 21:17:43 [forge-builder] build complete
```

### Poll build status

`GET /api/v1/venvs/{id}` lazily reads the `CIBuild` CR and updates the DB row on the fly:

```bash
curl -s $BASE/api/v1/venvs/1 | python3 -m json.tool
```

While building:

```json
{
    "id": 1,
    "status": "BUILDING",
    ...
}
```

After success:

```json
{
    "id": 1,
    "status": "SUCCESS",
    "ciBuildName": "forge-venv-1",
    "indexArtifactId": 42,
    "indexArtifactVersion": "1.0.0",
    ...
}
```

### Fetch logs via API

```bash
curl -s $BASE/api/v1/venvs/1/logs
```

Returns the builder pod's stdout as plain text. Returns `204 No Content` if the pod is still in `Pending` phase.

---

## 4. List builds

```bash
# All builds (newest first)
curl -s "$BASE/api/v1/venvs" | python3 -m json.tool

# Filter by status
curl -s "$BASE/api/v1/venvs?status=SUCCESS" | python3 -m json.tool

# Filter by name substring (case-insensitive)
curl -s "$BASE/api/v1/venvs?name=data" | python3 -m json.tool

# Filter by creator (SA username, e.g. system:serviceaccount:fusion/fusion-spectra)
curl -s "$BASE/api/v1/venvs?creatorId=system:serviceaccount:fusion/fusion-spectra" \
  | python3 -m json.tool

# Sort oldest first
curl -s "$BASE/api/v1/venvs?sortBy=createdAt&sortDir=asc" | python3 -m json.tool

# Paginate: page 1 (0-indexed), 5 items per page
curl -s "$BASE/api/v1/venvs?page=1&pageSize=5" | python3 -m json.tool
```

Response envelope:

```json
{
    "items": [ ... ],
    "total": 42,
    "page": 0,
    "pageSize": 20
}
```

---

## 5. Error cases

### Conflict — same name and version already exists

```bash
curl -s -X POST $BASE/api/v1/venvs \
  -F "name=data-env" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/requirements.txt" \
  | python3 -m json.tool
```

```json
{ "error": "venv 'data-env:1.0.0' already exists" }
```

HTTP `409 Conflict`. Submit with a different version string.

### Version already in registry

If `data-env:1.0.0` was successfully uploaded to fusion-index in a previous run but the DB row was lost, the registry check fires before the DB insert:

```json
{ "error": "version 1.0.0 already exists for data-env in registry" }
```

HTTP `409 Conflict`.

### Validation failure on submit

`POST /api/v1/venvs` runs the same validation as `/validate`. If the file is invalid the request is rejected before any artifact or DB record is created:

HTTP `400 Bad Request` with the violation list.

---

## 6. Kubernetes inspection

```bash
# All CIBuild objects
kubectl get cibuild -n fusion

# All build Jobs
kubectl get jobs -n fusion -l app.kubernetes.io/managed-by=fusion-forge

# All build pods (including completed)
kubectl get pods -n fusion -l app.kubernetes.io/managed-by=fusion-forge

# Operator logs
kubectl logs -n fusion deployment/fusion-forge-operator --tail=30

# Server logs
kubectl logs -n fusion deployment/fusion-forge-server --tail=30
```

---

## 7. Building a larger environment

```bash
cat > /tmp/ml-requirements.txt << 'EOF'
numpy==1.26.4
pandas==2.2.2
scikit-learn==1.5.0
matplotlib==3.9.0
scipy==1.13.1
joblib==1.4.2
threadpoolctl==3.5.0
python-dateutil==2.9.0
pytz==2024.1
six==1.16.0
contourpy==1.2.1
cycler==0.12.1
fonttools==4.53.1
kiwisolver==1.4.5
packaging==24.1
pillow==10.3.0
pyparsing==3.1.2
EOF

curl -s -X POST $BASE/api/v1/venvs \
  -F "name=ml-env" \
  -F "version=1.0.0" \
  -F "description=scikit-learn + matplotlib stack" \
  -F "requirements=@/tmp/ml-requirements.txt" \
  | python3 -m json.tool

# Monitor
watch -n 5 "curl -s $BASE/api/v1/venvs/2 | python3 -m json.tool"
```

Larger environments (scikit-learn with numpy deps) typically finish in 60–90 seconds on minikube.

---

## 8. Teardown

```bash
# Uninstall the Helm chart
helm uninstall fusion-forge -n fusion

# Or if deployed with raw manifests (make deploy):
make undeploy

# Delete PVC (drops all build history and PostgreSQL data)
kubectl delete pvc -n fusion -l app.kubernetes.io/name=fusion-forge

# Delete the CRD
kubectl delete -f config/crd/bases/
```
