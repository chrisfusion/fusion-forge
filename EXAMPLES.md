# API Examples

All examples assume the server is port-forwarded to `http://localhost:18080`.  
Git build endpoints accept `application/json`. Requirements build endpoints use `multipart/form-data`.

---

## Health probes

```bash
# Liveness — always 200 when the process is running
curl http://localhost:18080/q/health/live

# Readiness — 200 when DB is reachable, 503 otherwise
curl http://localhost:18080/q/health/ready
```

---

## Requirements builds (`/api/v1/venvs`)

### Validate without building

```bash
echo "numpy==1.26.4
pandas==2.2.1" > /tmp/req.txt

curl -s -X POST http://localhost:18080/api/v1/venvs/validate \
  -F name=mypackage \
  -F version=1.0.0 \
  -F description="My data package" \
  -F requirements=@/tmp/req.txt | jq .
```

Success (`200`):
```json
{ "valid": true, "violations": [] }
```

Failure (`422`):
```json
{
  "valid": false,
  "violations": [
    { "line": 1, "content": "numpy>=1.0", "message": "exact version pin required — use ==" }
  ]
}
```

### Submit a build

```bash
curl -s -X POST http://localhost:18080/api/v1/venvs \
  -F name=mypackage \
  -F version=1.0.0 \
  -F description="My data package" \
  -F requirements=@/tmp/req.txt | jq .
```

Response (`202 Accepted`):
```json
{
  "id": 42,
  "name": "mypackage",
  "version": "1.0.0",
  "description": "My data package",
  "status": "PENDING",
  "buildType": "requirements",
  "ciBuildName": "forge-venv-42",
  "createdAt": "2026-04-18T10:00:00Z",
  "updatedAt": "2026-04-18T10:00:00Z"
}
```

### Poll for completion

```bash
curl -s http://localhost:18080/api/v1/venvs/42 | jq .status
# "PENDING" → "BUILDING" → "SUCCESS" | "FAILED"
```

### Fetch builder logs

```bash
curl -s http://localhost:18080/api/v1/venvs/42/logs
```

### List builds

```bash
# All builds, newest first
curl -s "http://localhost:18080/api/v1/venvs" | jq .

# Filter by status and name, page 2
curl -s "http://localhost:18080/api/v1/venvs?status=SUCCESS&name=mypackage&page=1&pageSize=10" | jq .
```

Response:
```json
{
  "items": [ ... ],
  "total": 57,
  "page": 1,
  "pageSize": 10
}
```

---

## Git builds (`/api/v1/gitbuilds`)

### Validate — manual name and version

```bash
curl -s -X POST http://localhost:18080/api/v1/gitbuilds/validate \
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
curl -s -X POST http://localhost:18080/api/v1/gitbuilds/validate \
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
curl -s -X POST http://localhost:18080/api/v1/gitbuilds/validate \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/myapp",
    "repo_ref": "main",
    "metadata_source": "full"
  }' | jq .
```

### Submit a build — manual metadata

```bash
curl -s -X POST http://localhost:18080/api/v1/gitbuilds \
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
  "ciBuildName": "forge-git-7",
  "createdAt": "2026-04-18T11:00:00Z",
  "updatedAt": "2026-04-18T11:00:00Z"
}
```

### Submit a build — full metadata from pyproject.toml

```bash
curl -s -X POST http://localhost:18080/api/v1/gitbuilds \
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
curl -s -X POST http://localhost:18080/api/v1/gitbuilds \
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

Use `project_dir` when the Python project lives in a subdirectory of a larger repository. `pyproject.toml`, `src/`, and `entrypoint_file` are all resolved relative to `project_dir`.

```bash
curl -s -X POST http://localhost:18080/api/v1/gitbuilds \
  -H "Content-Type: application/json" \
  -d '{
    "repo_url": "https://github.com/org/monorepo",
    "repo_ref": "main",
    "project_dir": "services/myapp",
    "metadata_source": "full",
    "entrypoint_file": "app.py"
  }' | jq .
```

With this configuration:
- `pyproject.toml` is expected at `services/myapp/pyproject.toml`
- `src/` is expected at `services/myapp/src/`
- The entrypoint is uploaded from `services/myapp/app.py`

### List git builds

```bash
curl -s "http://localhost:18080/api/v1/gitbuilds?status=SUCCESS&sortBy=updatedAt&sortDir=desc" | jq .
```

### Fetch builder logs

```bash
curl -s http://localhost:18080/api/v1/gitbuilds/7/logs
```

---

## Error responses

| Status | Meaning |
|---|---|
| `400` | Bad request — missing required fields or invalid format |
| `409` | Conflict — a build for `name:version` already exists (DB or registry) |
| `422` | Validation failed — violation list returned |
| `500` | Internal error — check server logs |

All error bodies:
```json
{ "error": "human-readable message" }
```

---

## List query parameters (both endpoints)

| Parameter | Default | Description |
|---|---|---|
| `page` | `0` | Page index (0-based) |
| `pageSize` | `20` | Items per page (max 100) |
| `status` | — | `PENDING`, `BUILDING`, `SUCCESS`, `FAILED` |
| `name` | — | Case-insensitive substring match |
| `creatorId` | — | Exact SA username match |
| `sortBy` | `createdAt` | `createdAt`, `updatedAt`, `name`, `version`, `status` |
| `sortDir` | `desc` | `asc` or `desc` |
