# fusion-forge — Installation Guide

This guide covers deploying fusion-forge to a Kubernetes cluster using the Helm chart in `deployment/`.

---

## Prerequisites

- Kubernetes cluster (minikube for local dev)
- `kubectl` configured to the target cluster
- `helm` 3.x
- `fusion-index` already deployed in the `fusion` namespace — fusion-forge stores artifacts there
- `make` and `docker` available (for image builds)

---

## 1. Build images

Both images must be available in the cluster's container registry (or the local daemon for minikube).

### minikube

```bash
# Point Docker at minikube's daemon
eval $(minikube docker-env)

# Build main image (contains /server and /operator)
make docker-build IMG=fusion-forge:local

# Build builder pod image (Go uploader + Python 3.12)
make docker-build-builder BUILDER_IMG=fusion-venv-builder:local
```

### Remote registry

```bash
docker build -t registry.example.com/fusion-forge:0.1.0 .
docker build -t registry.example.com/fusion-venv-builder:0.1.0 builder/
docker push registry.example.com/fusion-forge:0.1.0
docker push registry.example.com/fusion-venv-builder:0.1.0
```

---

## 2. Install the CRD

The `CIBuild` CRD is bundled in `deployment/crds/` and installed automatically by Helm before any templates are rendered. No manual step required.

If you need to install it separately (e.g. cluster admin pre-provisioning):

```bash
kubectl apply -f config/crd/bases/
```

> **Note:** After editing `api/v1alpha1/` types, regenerate the CRD with `make generate` and copy
> the updated file to `deployment/crds/` before packaging a new chart version.

---

## 3. Install with Helm

### minikube (local images)

```bash
helm upgrade --install fusion-forge deployment/ \
  --namespace fusion \
  --set server.image.tag=local \
  --set operator.image.tag=local \
  --set server.config.builderImage=fusion-venv-builder:local \
  --wait --timeout 3m
```

### Production

```bash
helm upgrade --install fusion-forge deployment/ \
  --namespace fusion \
  --values my-values.yaml \
  --wait --timeout 5m
```

Where `my-values.yaml` overrides at minimum:

```yaml
server:
  image:
    repository: registry.example.com/fusion-forge
    tag: "0.1.0"
    pullPolicy: IfNotPresent
  config:
    builderImage: registry.example.com/fusion-venv-builder:0.1.0

operator:
  image:
    repository: registry.example.com/fusion-forge
    tag: "0.1.0"

postgresql:
  auth:
    password: "changeme-strong-password"
```

---

## 4. Verify the deployment

```bash
# All pods should be Running
kubectl get pods -n fusion -l app.kubernetes.io/name=fusion-forge

# Health checks
kubectl port-forward -n fusion service/fusion-forge 18080:8080 --address 127.0.0.1 &
curl http://localhost:18080/q/health/live
curl http://localhost:18080/q/health/ready
```

Expected output: `{"status":"UP"}` for both probes.

---

## 5. Key configuration options

All options are in `deployment/values.yaml`. Common overrides:

### External PostgreSQL

```yaml
postgresql:
  enabled: false
  external:
    host: pg.example.com
    port: 5432
    database: fusion_forge
    username: forge_user
    existingSecret: my-pg-secret   # must have key "password"
```

### K8s SA token auth

```yaml
auth:
  enabled: true
  audience: "fusion-forge"
  allowedServiceAccounts:
    - fusion/fusion-spectra
    - fusion/ci-runner

server:
  config:
    authEnabled: "true"
    authAudience: "fusion-forge"
    authAllowedSAs: "fusion/fusion-spectra,fusion/ci-runner"
```

### Custom validation rules

Mount a `forge-rules.yaml` via a ConfigMap and set the path:

```yaml
server:
  config:
    forgeRulesFile: /etc/forge/rules.yaml
```

Add a volume + volumeMount to `server.extraVolumes` / `server.extraVolumeMounts` in the deployment, or patch the Deployment after install.

### Ingress

```yaml
ingress:
  enabled: true
  className: nginx
  host: forge.fusion.example.com
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "50m"
  tls:
    enabled: true
    secretName: fusion-forge-tls
```

### Resource tuning

```yaml
server:
  resources:
    requests:
      cpu: 500m
      memory: 256Mi
    limits:
      cpu: "2"
      memory: 512Mi

operator:
  resources:
    requests:
      cpu: 200m
      memory: 128Mi
    limits:
      cpu: "1"
      memory: 256Mi
```

---

## 6. Upgrading

```bash
# Rebuild images with new tag, then:
helm upgrade fusion-forge deployment/ \
  --namespace fusion \
  --set server.image.tag=0.2.0 \
  --set operator.image.tag=0.2.0 \
  --wait
```

Migrations run automatically on server startup via golang-migrate. They are idempotent — safe to run multiple times.

---

## 7. Uninstalling

```bash
helm uninstall fusion-forge -n fusion
```

This removes all chart-managed resources. The CRD and PersistentVolumeClaims are **not** deleted by default (Helm does not delete CRDs on uninstall, and PVCs require manual cleanup):

```bash
# Delete PostgreSQL data (destructive — all build history lost)
kubectl delete pvc -n fusion -l app.kubernetes.io/name=fusion-forge

# Delete the CRD (removes all CIBuild objects cluster-wide)
kubectl delete -f config/crd/bases/
```

---

## Makefile targets (dev convenience)

| Target | Description |
|---|---|
| `make generate` | Regenerate deepcopy functions and CRD YAML |
| `make build` | Compile `bin/server` and `bin/operator` |
| `make test` | Run unit tests |
| `make docker-build` | Build main image inside minikube |
| `make docker-build-builder` | Build builder image inside minikube |
| `make install-crds` | `kubectl apply -f config/crd/bases/` |
| `make deploy` | Install CRDs + RBAC + raw manifests from `k8s/` |
| `make minikube-deploy` | Full cycle: build both images + deploy |
| `make undeploy` | Remove raw manifests (not the Helm chart) |
