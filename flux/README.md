# Flux GitOps — fusion-forge

Three Flux-managed Helm installations of fusion-forge:

| Environment | Namespace | PostgreSQL | Auth |
|---|---|---|---|
| dev | `dev-fusion` | internal (bundled) | disabled |
| staging | `dev-staging-fusion` | external | enabled |
| prod | `prod-fusion` | external | enabled |

---

## Directory structure

```
flux/
  sources/
    helmrepository.yaml          # Helm chart registry
    imagerepository.yaml         # Container image registries (forge + builder)
    imagepolicy.yaml             # Semver policy: always highest tag
    imageupdateautomation.yaml   # Auto-commits tag updates back to this repo
  environments/
    dev/
      namespace.yaml
      helmrelease.yaml
    staging/
      namespace.yaml
      helmrelease.yaml
    prod/
      namespace.yaml
      helmrelease.yaml
  clusters/
    dev.yaml      # Flux Kustomizations — apply to dev cluster
    staging.yaml  # Flux Kustomizations — apply to staging cluster
    prod.yaml     # Flux Kustomizations — apply to prod cluster
```

---

## TODOs before first deploy

1. **`flux/sources/helmrepository.yaml`** — replace `url` with your chart registry URL
2. **`flux/sources/imagerepository.yaml`** — replace both `image` values with your registry paths
3. **All `helmrelease.yaml` files** — replace `registry.example.com/fusion/...` with actual image paths
4. **`flux/environments/staging/helmrelease.yaml`** — set `postgresql.external.host` and `authAllowedSAs`
5. **`flux/environments/prod/helmrelease.yaml`** — set `postgresql.external.host` and `authAllowedSAs`
6. **`flux/sources/imageupdateautomation.yaml`** — set `git.checkout.ref.branch` and `git.push.branch` to your default branch

---

## Pre-requisites per cluster

### All clusters
Create the image pull secret in each namespace before the first Flux reconcile:
```bash
kubectl create secret docker-registry registry-credentials \
  --docker-server=registry.example.com \
  --docker-username=<user> \
  --docker-password=<token> \
  -n <namespace>
```

### Staging and production only
Create the PostgreSQL credentials secret before the first Helm install:
```bash
# Staging
kubectl create secret generic fusion-forge-db-secret \
  --from-literal=password=<password> \
  -n dev-staging-fusion

# Production
kubectl create secret generic fusion-forge-db-secret \
  --from-literal=password=<password> \
  -n prod-fusion
```

---

## Bootstrap

Bootstrap Flux on each cluster, pointing it at the cluster-specific path in this repository.

```bash
# Dev cluster
flux bootstrap github \
  --owner=<your-org> \
  --repository=fusion-forge \
  --branch=main \
  --path=flux/clusters \
  --personal

# Then apply the cluster entry point
kubectl apply -f flux/clusters/dev.yaml
```

Or, if you manage bootstrap manually:
```bash
# After flux bootstrap is complete on a cluster:
kubectl apply -f flux/clusters/dev.yaml      # dev
kubectl apply -f flux/clusters/staging.yaml  # staging
kubectl apply -f flux/clusters/prod.yaml     # prod
```

Each cluster file installs two Flux `Kustomization` objects:
1. `fusion-forge-sources` — applies `flux/sources/` (HelmRepository, ImagePolicies)
2. `fusion-forge-{env}` — applies `flux/environments/{env}/` (Namespace, HelmRelease); depends on sources

---

## Image version tracking

The `ImageUpdateAutomation` resource scans image registries every 30 minutes and automatically commits updated tags back to this repo whenever a higher semver version is published.

Tags in HelmRelease values carry markers that the automation patches in place:
```yaml
tag: "1.2.3" # {"$imagepolicy": "flux-system:fusion-forge:tag"}
```

Chart versions also auto-track: `version: ">=0.0.0"` means Flux always upgrades to the highest published chart version on the next reconcile interval.

---

## Check status

```bash
# Sources
flux get sources helm -n flux-system
flux get images all -n flux-system

# Releases
flux get helmreleases -A

# Kustomizations
flux get kustomizations -n flux-system
```
