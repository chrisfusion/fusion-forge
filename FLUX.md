# Flux Deployment Guide

This document walks through every step required to deploy fusion-forge with Flux across three environments: **dev**, **staging**, and **production**.

## Environments

| Environment | Namespace | PostgreSQL | Auth | Replicas |
|---|---|---|---|---|
| dev | `dev-fusion` | internal (bundled) | disabled | 1 |
| staging | `dev-staging-fusion` | external | enabled | 1 |
| prod | `prod-fusion` | external | enabled | 2 |

Each environment maps to one cluster. The Flux configuration lives in `flux/` inside this repository.

---

## Prerequisites

Install these tools on the machine you use to operate the clusters:

```bash
# Flux CLI
curl -s https://fluxcd.io/install.sh | sudo bash

# Verify minimum version (2.x required)
flux version --client

# kubectl — configured for each target cluster
kubectl version --client
```

Flux requires the following to be available on each cluster before bootstrap:
- Kubernetes 1.28 or newer
- A Git repository accessible over HTTPS or SSH (this repo)
- A personal access token or deploy key with read/write access to the repository (so `ImageUpdateAutomation` can push tag-bump commits)

---

## Step 1 — Fill in the TODOs

Before deploying anything, replace all placeholder values in the Flux manifests.

### 1a. Chart registry

Edit `flux/sources/helmrepository.yaml` and set the URL of your Helm chart registry:

```yaml
spec:
  url: https://charts.example.com  # ← replace
```

### 1b. Container image registry

Edit `flux/sources/imagerepository.yaml` and set the full image paths for both images:

```yaml
# fusion-forge server + operator image
spec:
  image: registry.example.com/fusion/fusion-forge  # ← replace

# builder image
spec:
  image: registry.example.com/fusion/fusion-venv-builder  # ← replace
```

### 1c. Image tags in HelmRelease values

Each `helmrelease.yaml` has four occurrences of the placeholder registry path — two for the server tag, one for the operator tag, and one for the builder image. Replace all of them:

```bash
# Preview what needs changing (run from repo root)
grep -r "registry.example.com" flux/environments/
```

Replace `registry.example.com/fusion/fusion-forge` and `registry.example.com/fusion/fusion-venv-builder` with your actual registry paths. Keep the `# {"$imagepolicy": ...}` markers intact — they are required for automatic tag updates.

### 1d. External PostgreSQL hosts

Edit `flux/environments/staging/helmrelease.yaml`:
```yaml
postgresql:
  external:
    host: postgres-staging.example.com  # ← replace
```

Edit `flux/environments/prod/helmrelease.yaml`:
```yaml
postgresql:
  external:
    host: postgres-prod.example.com  # ← replace
```

### 1e. Allowed service accounts (staging + prod)

For staging and production, set which Kubernetes service accounts are permitted to call the API. Format: `namespace/name`.

```yaml
# flux/environments/staging/helmrelease.yaml
config:
  authAllowedSAs: "dev-staging-fusion/fusion-spectra"  # ← replace

# flux/environments/prod/helmrelease.yaml
config:
  authAllowedSAs: "prod-fusion/fusion-spectra"  # ← replace
```

Leave `authAllowedSAs` empty to accept any valid service account token.

### 1f. Git branch for image automation

Edit `flux/sources/imageupdateautomation.yaml` and set your default branch name:

```yaml
git:
  checkout:
    ref:
      branch: main  # ← replace if your default branch is not "main"
  push:
    branch: main    # ← replace
```

---

## Step 2 — Create pre-existing secrets

These secrets must exist in the target namespace **before** the first Helm install. Flux will not create them — it only references them.

### Image pull secret (all clusters / all namespaces)

Create the registry credentials secret in each namespace after the namespace exists. The simplest approach is to apply `namespace.yaml` first, then create the secret:

```bash
# Dev
kubectl apply -f flux/environments/dev/namespace.yaml
kubectl create secret docker-registry registry-credentials \
  --docker-server=registry.example.com \
  --docker-username=<user> \
  --docker-password=<token> \
  -n dev-fusion

# Staging
kubectl apply -f flux/environments/staging/namespace.yaml
kubectl create secret docker-registry registry-credentials \
  --docker-server=registry.example.com \
  --docker-username=<user> \
  --docker-password=<token> \
  -n dev-staging-fusion

# Production
kubectl apply -f flux/environments/prod/namespace.yaml
kubectl create secret docker-registry registry-credentials \
  --docker-server=registry.example.com \
  --docker-username=<user> \
  --docker-password=<token> \
  -n prod-fusion
```

### PostgreSQL credentials (staging + prod only)

The Helm chart reads `fusion-forge-db-secret` and uses the `password` key. Create it before Flux reconciles:

```bash
# Staging
kubectl create secret generic fusion-forge-db-secret \
  --from-literal=password=<staging-db-password> \
  -n dev-staging-fusion

# Production
kubectl create secret generic fusion-forge-db-secret \
  --from-literal=password=<prod-db-password> \
  -n prod-fusion
```

---

## Step 3 — Bootstrap Flux

Bootstrap must be run once per cluster. It installs the Flux controllers, creates a `GitRepository` pointing at this repo, and sets up a reconciliation loop.

The `--path` flag points Flux at the cluster-specific entry file in `flux/clusters/`.

### Dev cluster

```bash
# Make sure kubectl points at the dev cluster
kubectl config use-context <dev-cluster-context>

flux bootstrap github \
  --owner=<github-org-or-user> \
  --repository=fusion-forge \
  --branch=main \
  --path=flux/clusters \
  --personal
```

After bootstrap, apply the dev cluster Kustomizations:

```bash
kubectl apply -f flux/clusters/dev.yaml
```

### Staging cluster

```bash
kubectl config use-context <staging-cluster-context>

flux bootstrap github \
  --owner=<github-org-or-user> \
  --repository=fusion-forge \
  --branch=main \
  --path=flux/clusters \
  --personal

kubectl apply -f flux/clusters/staging.yaml
```

### Production cluster

```bash
kubectl config use-context <prod-cluster-context>

flux bootstrap github \
  --owner=<github-org-or-user> \
  --repository=fusion-forge \
  --branch=main \
  --path=flux/clusters \
  --personal

kubectl apply -f flux/clusters/prod.yaml
```

> **SSH instead of HTTPS?** Replace `flux bootstrap github` with `flux bootstrap git --url=ssh://git@github.com/<org>/fusion-forge` and supply `--private-key-file`.

---

## Step 4 — Verify the deployment

Run these checks after applying each cluster file. Reconciliation normally completes within 2–3 minutes.

### Check Kustomizations

```bash
flux get kustomizations -n flux-system
```

Expected output (dev cluster example):
```
NAME                    READY   MESSAGE
flux-system             True    Applied revision: main@sha1:...
fusion-forge-sources    True    Applied revision: main@sha1:...
fusion-forge-dev        True    Applied revision: main@sha1:...
```

### Check HelmRepository source

```bash
flux get sources helm -n flux-system
```

```
NAME              READY   MESSAGE
fusion-platform   True    stored artifact: revision 'digest/...'
```

### Check HelmRelease

```bash
# Dev
flux get helmreleases -n dev-fusion

# Staging
flux get helmreleases -n dev-staging-fusion

# Production
flux get helmreleases -n prod-fusion
```

```
NAME          READY   MESSAGE
fusion-forge  True    Helm install succeeded for release dev-fusion/fusion-forge
```

### Check image policies

```bash
flux get images all -n flux-system
```

```
NAME                    READY   LATEST IMAGE
fusion-forge            True    registry.example.com/fusion/fusion-forge:1.2.3
fusion-venv-builder     True    registry.example.com/fusion/fusion-venv-builder:1.2.3
```

### Smoke-test the API

Port-forward to the desired environment and call the health endpoint:

```bash
# Dev example
kubectl port-forward -n dev-fusion service/fusion-forge 18080:8080 --address 127.0.0.1
curl http://localhost:18080/q/health/ready
# {"status":"UP"}
```

---

## Step 5 — How automatic version updates work

Once running, Flux handles version upgrades without manual intervention.

### Chart version

`HelmRelease` uses `version: ">=0.0.0"` in the chart spec. Every 10 minutes Flux checks the `HelmRepository` for a higher chart version. If one exists, it triggers a Helm upgrade automatically.

### Image tags

1. `ImageRepository` polls the container registry every 10 minutes.
2. `ImagePolicy` selects the tag with the highest semver value.
3. `ImageUpdateAutomation` finds the `# {"$imagepolicy": ...}` marker in each `helmrelease.yaml` and patches the tag value in place.
4. It commits and pushes the change to the repository.
5. Flux detects the new commit, reconciles the `Kustomization`, and triggers a Helm upgrade.

The result is a fully automated path from `docker push registry.example.com/fusion/fusion-forge:1.3.0` to a live Helm upgrade in all three clusters — with a git commit recording each change.

---

## Day-2 operations

### Force an immediate reconcile

```bash
# All kustomizations on the current cluster
flux reconcile kustomization fusion-forge-dev --with-source

# A specific HelmRelease
flux reconcile helmrelease fusion-forge -n dev-fusion
```

### Suspend / resume a release (e.g. for maintenance)

```bash
# Suspend — Flux stops reconciling but leaves the existing Helm release running
flux suspend helmrelease fusion-forge -n prod-fusion

# Resume
flux resume helmrelease fusion-forge -n prod-fusion
```

### Roll back to a previous Helm revision

```bash
# List Helm release history
helm history fusion-forge -n prod-fusion

# Roll back to revision 3
helm rollback fusion-forge 3 -n prod-fusion
```

After a manual rollback, Flux will attempt to reconcile forward again on the next interval. To keep it rolled back, suspend the HelmRelease first.

### View Flux events and errors

```bash
# Events for a specific HelmRelease
flux events --for HelmRelease/fusion-forge -n prod-fusion

# All Flux controller logs
flux logs --all-namespaces
```

### Manually trigger an image scan

```bash
flux reconcile image repository fusion-forge -n flux-system
flux reconcile image policy fusion-forge -n flux-system
```

---

## Troubleshooting

| Symptom | Likely cause | Resolution |
|---|---|---|
| `HelmRelease` stuck in `Progressing` | Chart version not found in HelmRepository | Check `flux get sources helm` — verify chart URL and that the chart has been pushed |
| `HelmRelease` fails with `secret not found` | Pre-existing secret missing | Create `registry-credentials` or `fusion-forge-db-secret` in the target namespace (Step 2) |
| Image policy shows no latest image | Registry not reachable or wrong image path | Verify `imagerepository.yaml` image path and that `registry-credentials` secret is in `flux-system` |
| `ImageUpdateAutomation` not committing | Bot has no push access | Ensure the Git token used during bootstrap has write permission to the repository |
| Pods stuck in `ImagePullBackOff` | `registry-credentials` missing in workload namespace | Create the pull secret in the workload namespace (Step 2) |
| Flux reconciles but Helm upgrade fails | Chart values error | Run `flux get helmreleases -A` for the message, then `flux events --for HelmRelease/fusion-forge -n <ns>` for detail |
