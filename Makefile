# SPDX-License-Identifier: GPL-3.0-or-later

CONTROLLER_GEN ?= $(HOME)/go/bin/controller-gen
IMG            ?= fusion-forge:latest
BUILDER_IMG    ?= fusion-venv-builder:latest
NAMESPACE      ?= fusion

.PHONY: all build test generate manifests docker-build docker-build-builder \
        install-crds install-rbac deploy undeploy minikube-deploy create-namespace

all: generate build

## Generate deepcopy functions and CRD YAML manifests.
generate:
	$(CONTROLLER_GEN) object:headerFile="" paths="./api/..."
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=config/crd/bases

## Build both Go binaries.
build:
	CGO_ENABLED=0 go build -o bin/server   ./cmd/server/
	CGO_ENABLED=0 go build -o bin/operator ./cmd/operator/

## Run tests.
test:
	go test ./... -v

## Build and load the main image into minikube.
docker-build:
	eval $$(minikube docker-env) && docker build -t $(IMG) .

## Build and load the builder image into minikube.
docker-build-builder:
	eval $$(minikube docker-env) && docker build -t $(BUILDER_IMG) builder/

## Create the fusion namespace (idempotent).
create-namespace:
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -

## Install CRDs into the cluster.
install-crds:
	kubectl apply -f config/crd/bases/

## Apply RBAC resources.
install-rbac:
	kubectl apply -f k8s/rbac.yaml

## Deploy both the server and operator.
deploy: install-crds install-rbac
	kubectl apply -f k8s/deployment.yaml

## Remove the server and operator deployments.
undeploy:
	kubectl delete -f k8s/deployment.yaml --ignore-not-found
	kubectl delete -f k8s/rbac.yaml       --ignore-not-found
	kubectl delete -f config/crd/bases/   --ignore-not-found

## Full minikube dev cycle: build images, install CRDs + RBAC, deploy.
minikube-deploy: create-namespace docker-build docker-build-builder deploy
