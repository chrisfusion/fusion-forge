# fusion-forge

Asynchronous Python virtual environment builder for the fusion platform.

fusion-forge accepts a `requirements.txt` **or** a Git repository URL, builds a Python 3.12 virtual environment inside a Kubernetes Job, archives it as a `.tar.gz`, and registers the result in the [fusion-index](../fusion-index/) artifact registry.

---

## Features

- **Requirements builds** — upload a `requirements.txt`; forge validates, builds, and registers a pinned venv
- **Git builds** — point at any Git repository; forge clones, builds a wheel from `pyproject.toml`, and installs it
- **Monorepo support** — optional `project_dir` field targets a subdirectory within a repository
- **Metadata auto-detection** — server-side `pyproject.toml` parsing extracts `name` and/or `version` before the build starts (`metadata_source`: `manual` / `version` / `full`)
- **Async lifecycle** — builds run as Kubernetes Jobs managed by a co-located operator; status is lazily synced on `GET`
- **Validation endpoints** — pre-flight check any request without starting a build or touching the registry
- **K8s SA token auth** — optional `TokenReview`-based authentication

---

## API overview

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/venvs` | List requirements builds |
| `POST` | `/api/v1/venvs` | Submit a requirements build |
| `POST` | `/api/v1/venvs/validate` | Validate without building |
| `GET` | `/api/v1/venvs/:id` | Get status (lazily syncs CR) |
| `GET` | `/api/v1/venvs/:id/logs` | Fetch builder pod logs |
| `GET` | `/api/v1/gitbuilds` | List git builds |
| `POST` | `/api/v1/gitbuilds` | Submit a git build |
| `POST` | `/api/v1/gitbuilds/validate` | Validate without building |
| `GET` | `/api/v1/gitbuilds/:id` | Get status (lazily syncs CR) |
| `GET` | `/api/v1/gitbuilds/:id/logs` | Fetch builder pod logs |
| `GET` | `/q/health/live` | Liveness probe |
| `GET` | `/q/health/ready` | Readiness probe (checks DB) |

---

## Quick start (minikube)

```bash
# Build images inside minikube
eval $(minikube docker-env)
make docker-build         IMG=fusion-forge:local
make docker-build-builder BUILDER_IMG=fusion-venv-builder:local

# Deploy
make minikube-deploy

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
```

---

## Stack

| Layer | Technology |
|---|---|
| Language | Go 1.25 |
| REST API | Gin |
| Persistence | PostgreSQL 16 · pgx/v5 · golang-migrate |
| Operator | controller-runtime v0.19 |
| Auth | Kubernetes SA TokenReview |
| Builder pod | Go binary + Python 3.12-slim |
| Deployment | Helm 3 (self-contained, no external subcharts) |
| License | GPL-3.0 |

---

## Documentation

| Document | Contents |
|---|---|
| [INSTALL.md](INSTALL.md) | Build images, Helm deploy, production configuration |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Component overview, data flow, CRD lifecycle, DB schema |
| [EXAMPLES.md](EXAMPLES.md) | Full curl examples for every endpoint |
