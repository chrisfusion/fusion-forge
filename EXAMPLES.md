# fusion-forge — API Examples

End-to-end walkthrough and reference for the fusion-forge REST API. Assumes the server is port-forwarded to `localhost:18080` (see [README.md](README.md)):

```bash
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1
```

```bash
BASE=http://localhost:18080
```

Auth is disabled by default (`AUTH_ENABLED=false`). If your deployment has auth enabled, add `-H "Authorization: Bearer <sa-token>"` to every request.

Requirements-build endpoints (`/api/v1/venvs`) use `multipart/form-data`; every other write endpoint (`gitbuilds`, `appbuilds`, `gitwatchers`, bulk maintenance) takes `application/json`.

---

## Health probes

```bash
# Liveness — always 200 when the process is running
curl -s $BASE/q/health/live

# Readiness — 200 when DB is reachable, 503 otherwise
curl -s $BASE/q/health/ready
```

Expected: `{"status":"UP"}` for both.

---

## 1. Requirements builds (`/api/v1/venvs`)

### Validate a requirements.txt (dry run)

Validation runs without creating any Kubernetes resources, DB rows, or registry entries.

```bash
cat > /tmp/requirements.txt << 'EOF'
requests==2.31.0
numpy==1.26.4
pandas==2.2.2
EOF

curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=data-env" \
  -F "version=1.0.0" \
  -F "requirements=@/tmp/requirements.txt" | jq .
```

Success (`200`):
```json
{ "valid": true, "violations": [] }
```

#### Validation failure examples

**Version specifier required:**

```bash
printf 'pandas\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" | jq .
```
```json
{
  "valid": false,
  "violations": [
    { "line": 1, "content": "pandas", "message": "version specifier is required (e.g. pandas==2.2.2)" }
  ]
}
```

**Inexact pin (when `require-exact-pinning: true`):**

```bash
printf 'numpy>=1.0\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" | jq .
```
```json
{
  "valid": false,
  "violations": [
    { "line": 1, "content": "numpy>=1.0", "message": "exact version pin required — use ==" }
  ]
}
```

**VCS dependency:**

```bash
printf 'git+https://github.com/org/repo.git\n' > /tmp/bad.txt
curl -s -X POST $BASE/api/v1/venvs/validate \
  -F "name=test" -F "version=1.0.0" -F "requirements=@/tmp/bad.txt" | jq .
```
```json
{
  "valid": false,
  "violations": [
    { "line": 1, "content": "git+https://github.com/org/repo.git", "message": "VCS and URL dependencies are not allowed" }
  ]
}
```

### Submit a build

```bash
curl -s -X POST $BASE/api/v1/venvs \
  -F "name=data-env" \
  -F "version=1.0.0" \
  -F "description=Minimal data science environment" \
  -F "requirements=@/tmp/requirements.txt" | jq .
```

Response (`202 Accepted`):
```json
{
  "id": 1,
  "name": "data-env",
  "version": "1.0.0",
  "description": "Minimal data science environment",
  "status": "PENDING",
  "buildType": "requirements",
  "pythonVersion": "3.12",
  "ciBuildName": "forge-venv-1",
  "indexArtifactId": 42,
  "indexArtifactVersion": "1.0.0",
  "createdAt": "2026-07-24T21:17:34Z",
  "updatedAt": "2026-07-24T21:17:34Z"
}
```

Note the `id` — use it to poll status and fetch logs. Add `-F python_version=3.10` to build against the Python 3.10 image instead of the default 3.12.

### Watch the build

```bash
# The operator creates a CIBuild CR and drives a Kubernetes Job from it
kubectl get cibuild forge-venv-1 -n fusion
kubectl get cibuild forge-venv-1 -n fusion -o yaml   # full spec + status

# Follow the builder pod logs directly
kubectl logs -n fusion -l job-name=forge-job-forge-venv-1 -f
```

### Poll for completion

```bash
curl -s $BASE/api/v1/venvs/1 | jq .status
# "PENDING" → "BUILDING" → "SUCCESS" | "FAILED"
```

`GET /api/v1/venvs/{id}` lazily reads the `CIBuild` CR and updates the DB row on the fly — no separate sync step needed.

### Fetch builder logs

```bash
curl -s $BASE/api/v1/venvs/1/logs
```

Returns the builder pod's stdout as plain text; `204 No Content` if the pod hasn't started yet.

### List builds

```bash
# All builds, newest first
curl -s "$BASE/api/v1/venvs" | jq .

# Filter by status and name, page 1
curl -s "$BASE/api/v1/venvs?status=SUCCESS&name=data&page=1&pageSize=10" | jq .
```

Response envelope:
```json
{ "items": [ ... ], "total": 57, "page": 0, "pageSize": 20 }
```

---

## 2. Git builds (`/api/v1/gitbuilds`)

### Validate — manual name and version

```bash
curl -s -X POST $BASE/api/v1/gitbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myapp",
    "version": "2.1.0",
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main"
  }' | jq .
```

### Validate — version from pyproject.toml

The server performs an in-memory git clone to fetch `pyproject.toml` and resolve the version.

```bash
curl -s -X POST $BASE/api/v1/gitbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myapp",
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "v2.1.0",
    "metadata_source": "version"
  }' | jq .
```

### Validate — name and version both from pyproject.toml

```bash
curl -s -X POST $BASE/api/v1/gitbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main",
    "metadata_source": "full"
  }' | jq .
```

### Submit a build — manual metadata

```bash
curl -s -X POST $BASE/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myapp",
    "version": "2.1.0",
    "description": "My application venv",
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main"
  }' | jq .
```

Response (`202 Accepted`):
```json
{
  "id": 7,
  "name": "myapp",
  "version": "2.1.0",
  "status": "PENDING",
  "buildType": "git",
  "repoUrl": "https://github.com/org/myapp",
  "repoRef": "main",
  "metadataSource": "manual",
  "pythonVersion": "3.12",
  "ciBuildName": "forge-git-7",
  "createdAt": "2026-07-24T11:00:00Z",
  "updatedAt": "2026-07-24T11:00:00Z"
}
```

### Submit a build — full metadata from pyproject.toml

```bash
curl -s -X POST $BASE/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main",
    "metadata_source": "full"
  }' | jq .
```

### Submit a build — with entrypoint file

The entrypoint (`app.py`) is uploaded to fusion-index as a second artifact alongside the venv archive. It is resolved relative to the project root (or `project_dir` when set).

```bash
curl -s -X POST $BASE/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "name": "myapp",
    "version": "2.1.0",
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main",
    "entrypoint_file": "app.py"
  }' | jq .
```

### Submit a build — monorepo subdirectory

`project_dir` targets a subdirectory of a larger repository; `pyproject.toml`, `src/`, and `entrypoint_file` are all resolved relative to it.

```bash
curl -s -X POST $BASE/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/monorepo",
    "repo_ref": "main",
    "project_dir": "services/myapp",
    "metadata_source": "full",
    "entrypoint_file": "app.py",
    "python_version": "3.10"
  }' | jq .
```

With this configuration `pyproject.toml` is expected at `services/myapp/pyproject.toml`, `src/` at `services/myapp/src/`, and the entrypoint at `services/myapp/app.py`.

### List git builds

```bash
curl -s "$BASE/api/v1/gitbuilds?status=SUCCESS&sortBy=updatedAt&sortDir=desc" | jq .
```

### Fetch builder logs

```bash
curl -s $BASE/api/v1/gitbuilds/7/logs
```

---

## 3. App builds (`/api/v1/appbuilds`)

App builds only need the repository coordinates — `name`, `version`, `builderImage`, and `runner` are all read from the repository's `metadata.yaml`. See `../fusion-testcases/simple_streamlit_template/` for a reference `metadata.yaml` / `requirements.txt` / `main.py` layout.

#### Multi-script builds via `files`

`metadata.yaml` accepts an optional `files` key controlling which loose Python files are uploaded to fusion-index alongside the venv archive, for repos with several standalone scripts (e.g. `extract.py`/`transform.py`/`load.py`) sharing one `requirements.txt` — each later picked as a different entrypoint by a separate consumer (e.g. a fusion-flux chain step), instead of one fixed `main.py`.

- **Key absent (default)** — unchanged legacy behavior: `main.py` is required and is the only file uploaded.
- **`files: []`** (present, empty) — auto-discovers and uploads every top-level `*.py` file (non-recursive; subdirectories are still bundled into the venv's `site-packages` as before). `main.py` is not required.
- **`files: [extract.py, transform.py, load.py]`** — uploads exactly those files (fails fast if any is missing). `main.py` is not required.

Filenames are validated the same way as `project_dir` — absolute paths and `..` path segments are rejected at fetch time (`gitutil.FetchAppMetadata`), since `metadata.yaml` content is repo-controlled and flows unsanitized into the builder pod's filesystem.

Do not put a per-consumer entrypoint selector under `runner.args` on an artifact meant for multiple different entrypoints — `runner.args` keys are injected as env vars unconditionally by downstream consumers and will silently win over any per-step override. Omit `runner` entirely (or set only `runner.type`, without `args`) for this shape. See `../fusion-testcases/testcases_v2/venv-builds/etl-pipeline/EXPLANATION.md` for the mechanism this avoids (documented there against fusion-weave's `codesource.EnvVars`), and `../fusion-testcases/testcases_v2/app-builds/` for the single-entrypoint `runner.args.ENTRYPOINT` pattern this complements.

### Validate

```bash
curl -s -X POST $BASE/api/v1/appbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/streamlit-app",
    "repo_ref": "main"
  }' | jq .
```

`422` if `metadata.yaml` is missing/invalid, the declared `builderImage` key isn't in the `builderImages` ConfigMap, or `name:version` already exists (DB or registry).

### Submit a build

```bash
curl -s -X POST $BASE/api/v1/appbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/streamlit-app",
    "repo_ref": "main"
  }' | jq .
```

Response (`202 Accepted`):
```json
{
  "id": 12,
  "name": "streamlit-app",
  "version": "1.0.0",
  "status": "PENDING",
  "buildType": "app",
  "repoUrl": "https://github.com/org/streamlit-app",
  "repoRef": "main",
  "runner": "streamlit",
  "ciBuildName": "forge-app-12",
  "createdAt": "2026-07-24T12:00:00Z",
  "updatedAt": "2026-07-24T12:00:00Z"
}
```

### Submit a build — monorepo subdirectory, layered on a base venvpack

```bash
curl -s -X POST $BASE/api/v1/appbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/monorepo",
    "repo_ref": "main",
    "project_dir": "apps/dashboard"
  }' | jq .
```

If `apps/dashboard/metadata.yaml` sets `basedependencies` to a venvpack URL, the builder seeds `venv/` from that archive before installing `requirements.txt` on top.

### List / fetch logs

```bash
curl -s "$BASE/api/v1/appbuilds?status=SUCCESS" | jq .
curl -s $BASE/api/v1/appbuilds/12/logs
```

---

## 4. GitWatcher CRUD (`/api/v1/gitwatchers`)

GitWatcher is a K8s CR only — there is no DB row for a watcher itself (only for the builds it triggers).

### Create — watch a git-build repo

```bash
curl -s -X POST $BASE/api/v1/gitwatchers \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-lib",
    "repo_url": "https://github.com/org/my-lib",
    "repo_ref": "main",
    "build_type": "git",
    "metadata_source": "full",
    "python_version": "3.12"
  }' | jq .
```

`POST` always does a live `FetchRemoteHEAD` pre-flight against the repository before creating the CR — it fails with `422` if the repo/ref is unreachable.

Response:
```json
{
  "name": "my-lib",
  "namespace": "fusion",
  "createdAt": "2026-07-24T09:00:00Z",
  "spec": {
    "repoURL": "https://github.com/org/my-lib",
    "repoRef": "main",
    "buildType": "git",
    "enabled": true,
    "metadataSource": "full",
    "pythonVersion": "3.12"
  },
  "status": { "phase": "", "consecutiveFailures": 0 }
}
```

### Create — watch an app-build repo on a private repository

```bash
kubectl create secret generic my-repo-token \
  --from-literal=token=<personal-access-token> -n fusion

curl -s -X POST $BASE/api/v1/gitwatchers \
  -H "Content-Type: application/json" \
  -d '{
    "name": "internal-dashboard",
    "repo_url": "https://gitea.internal/team/dashboard.git",
    "repo_ref": "main",
    "build_type": "app",
    "token_secret_ref": { "name": "my-repo-token", "key": "token" }
  }' | jq .
```

### List / get

```bash
curl -s "$BASE/api/v1/gitwatchers?page=0&pageSize=20" | jq .
curl -s $BASE/api/v1/gitwatchers/my-lib | jq .
```

Or via `kubectl` (short name `gw`):
```bash
kubectl get gw -n fusion
kubectl get gw my-lib -n fusion -o yaml
```

### Update — pause a watcher without deleting it

`PUT` replaces the full spec; only re-checks `FetchRemoteHEAD` when `repo_url` or `repo_ref` changes.

```bash
curl -s -X PUT $BASE/api/v1/gitwatchers/my-lib \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/my-lib",
    "repo_ref": "main",
    "build_type": "git",
    "enabled": false,
    "metadata_source": "full"
  }' | jq .
```

### Delete

```bash
curl -s -X DELETE $BASE/api/v1/gitwatchers/my-lib -w '%{http_code}\n'
```

### Watch it work

```bash
# Poll status — lastSeenCommit, lastBuiltVersion, consecutiveFailures
watch -n 10 "curl -s $BASE/api/v1/gitwatchers/my-lib | jq .status"

# Watcher logs
kubectl logs -n fusion deployment/fusion-forge-watcher --tail=50 -f
```

---

## 5. Bulk build maintenance (`/api/v1/builds`)

### Bulk delete old builds

Deletes only `FAILED`/`SUCCESS` builds (PENDING/BUILDING are rejected with `422`); at most 1000 rows per call.

```bash
curl -s -X DELETE $BASE/api/v1/builds \
  -H "Content-Type: application/json" \
  -d '{
    "statuses": ["FAILED", "SUCCESS"],
    "older_than": "2026-06-01T00:00:00Z",
    "build_type": "requirements"
  }' | jq .
```

Response:
```json
{ "deleted": [3, 4, 9], "failed": [] }
```

### Zombie cleanup

Reconciles PENDING/BUILDING rows whose CIBuild CR no longer exists in the cluster (e.g. after a manual `kubectl delete cibuild` or a cluster wipe).

```bash
curl -s -X POST $BASE/api/v1/builds/zombie-cleanup \
  -H "Content-Type: application/json" \
  -d '{ "older_than": "2026-07-01T00:00:00Z" }' | jq .
```

Response shape matches bulk delete: `{"deleted": [...], "failed": [...]}`.

---

## 6. Error cases

### Conflict — same name and version already exists

```bash
curl -s -X POST $BASE/api/v1/venvs \
  -F "name=data-env" -F "version=1.0.0" -F "requirements=@/tmp/requirements.txt" | jq .
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

`POST /api/v1/venvs`, `/gitbuilds`, and `/appbuilds` all run the same validation as their `/validate` counterpart — the request is rejected before any artifact or DB record is created.

| Status | Meaning |
|---|---|
| `400` | Bad request — missing required fields or invalid format |
| `409` | Conflict — a build for `name:version` already exists (DB or registry) |
| `422` | Validation failed — violation list returned |
| `500` | Internal error — check server logs |

All non-validation error bodies:
```json
{ "error": "human-readable message" }
```

### Retriggering a build for the same version after a failed cleanup

`DELETE /api/v1/builds` and zombie-cleanup are best-effort on fusion-index. If a version is left behind, the next trigger returns `409`. Clean it up manually:
```bash
curl -X DELETE http://localhost:18081/api/v1/artifacts/{id}/versions/{ver}
```

---

## 7. List query parameters (`venvs`, `gitbuilds`, `appbuilds`, `gitwatchers`)

| Parameter | Default | Description |
|---|---|---|
| `page` | `0` | Page index (0-based) |
| `pageSize` | `20` | Items per page (max 100) |
| `status` | — | `PENDING`, `BUILDING`, `SUCCESS`, `FAILED` (build endpoints only) |
| `name` | — | Case-insensitive substring match |
| `creatorId` | — | Exact SA username match (build endpoints only) |
| `sortBy` | `createdAt` | `createdAt`, `updatedAt`, `name`, `version`, `status` |
| `sortDir` | `desc` | `asc` or `desc` |

---

## 8. Kubernetes inspection

```bash
# All CIBuild / GitWatcher objects
kubectl get cibuild -n fusion
kubectl get gw -n fusion

# All build Jobs and pods (including completed)
kubectl get jobs -n fusion -l app.kubernetes.io/managed-by=fusion-forge
kubectl get pods -n fusion -l app.kubernetes.io/managed-by=fusion-forge

# Component logs
kubectl logs -n fusion deployment/fusion-forge-server   --tail=30
kubectl logs -n fusion deployment/fusion-forge-operator --tail=30
kubectl logs -n fusion deployment/fusion-forge-watcher  --tail=30
```

---

## 9. Building a larger environment

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
  -F "requirements=@/tmp/ml-requirements.txt" | jq .

# Monitor
watch -n 5 "curl -s $BASE/api/v1/venvs/2 | jq ."
```

Larger environments (scikit-learn with numpy deps) typically finish in 60–90 seconds on minikube.

---

## 10. Teardown

```bash
# Uninstall the Helm chart
helm uninstall fusion-forge -n fusion

# Or if deployed with raw manifests (make deploy):
make undeploy

# Delete PVC (drops all build history and PostgreSQL data)
kubectl delete pvc -n fusion -l app.kubernetes.io/name=fusion-forge

# Delete the CRDs (removes all CIBuild/GitWatcher objects cluster-wide)
kubectl delete -f config/crd/bases/
```
