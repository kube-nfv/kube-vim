NAME ?= kube-vim

IMG ?= ghcr.io/kube-nfv/kube-vim:latest

DEV ?= 0

K8S_VERSION ?= v1.31.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: all
all: build

##@ Development

.PHONY: manifests

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: mod-tidy
mod-tidy: ## Run go mod tidy against code.
	go mod tidy

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

##@ Build

.PHONY: build
build: fmt vet
	go build -o bin/kubevim cmd/kube-vim/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} -f dist/Dockerfile .

.PHONY: build-dist-manifests
build-dist-manifests:
	mkdir -p dist

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

HELM_PLUGINS ?= $(LOCALBIN)/helm-plugins
export HELM_PLUGINS
$(HELM_PLUGINS):
	mkdir -p $(HELM_PLUGINS)

## Tool Binaries
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
KIND ?= $(LOCALBIN)/kind
YQ = $(LOCALBIN)/yq
KUBE_OVN_INSTALL ?= $(LOCALBIN)/kube-ovn/install.sh
KUBE_VIRT_OPERATOR ?= $(LOCALBIN)/kube-virt/kubevirt-operator.yaml
KUBE_VIRT_CR       ?= $(LOCALBIN)/kube-virt/kubevirt-cr.yaml


KIND_VERSION ?= v0.23.0
YQ_VERSION ?= v4.44.1
GOLANGCI_LINT_VERSION ?= v1.59.1

KUBE_OVN_VERSION ?= v1.13.0
KUBE_VIRT_VERSION ?= v1.4.0

.PHONY: golangci-lint
golangci-lint: $(LOCALBIN)
	@test -x $(GOLANGCI_LINT) && $(GOLANGCI_LINT) version | grep -q $(GOLANGCI_LINT_VERSION) || \
	GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: kind
kind: $(LOCALBIN)
	@test -x $(KIND) && $(KIND) version | grep -q $(KIND_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kind@$(KIND_VERSION)

.PHONY: yq
yq: $(LOCALBIN)
	@test -x $(YQ) && $(YQ) version | grep -q $(YQ_VERSION) || \
	GOBIN=$(LOCALBIN) go install github.com/mikefarah/yq/v4@$(YQ_VERSION)

.PHONY: kube-ovn
kube-ovn: $(LOCALBIN)
	@test -x $(KUBE_OVN_INSTALL) || \
	wget -P $(LOCALBIN)/kube-ovn https://raw.githubusercontent.com/kubeovn/kubmaster/dist/images/install.sh; chmod +x $(KUBE_OVN_INSTALL)

.PHONY: kube-virt
kube-virt: $(LOCALBIN)
	@test -e $(KUBE_VIRT_OPERATOR) || \
	wget -P $(LOCALBIN)/kube-virt https://github.com/kubevirt/kubevirt/releases/download/$(KUBE_VIRT_VERSION)/kubevirt-operator.yaml
	@test -e $(KUBE_VIRT_CR) || \
	wget -P $(LOCALBIN)/kube-virt https://github.com/kubevirt/kubevirt/releases/download/$(KUBE_VIRT_VERSION)/kubevirt-cr.yaml
	


##@ Deployment

KIND_CLUSTER_NAME ?= kube-vim-kind
KIND_CONFIG ?=
ifeq ($(DEV), 1)
	KIND_CONFIG = dist/kind-dev.yaml
else
	KIND_CONFIG = dist/kind.yaml
endif


CONTROL_PLANE_TAINTS = node-role.kubernetes.io/master node-role.kubernetes.io/control-plane

.PHONY: kind-load
kind-load: docker-build kind kind-create ## Build and upload docker image to the local Kind cluster.
	$(KIND) load docker-image ${IMG} --name $(KIND_CLUSTER_NAME)

.PHONY: kind-create
kind-create: kind yq ## Create kubernetes cluster using Kind.
	@if ! $(KIND) get clusters | grep -q $(KIND_CLUSTER_NAME); then \
		$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image kindest/node:$(K8S_VERSION) --config $(KIND_CONFIG); \
	elif ! $(CONTAINER_TOOL) container inspect $$($(KIND) get nodes --name $(KIND_CLUSTER_NAME)) | $(YQ) e '.[0].Config.Image' | grep -q $(K8S_VERSION); then \
  		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
		$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image kindest/node:$(K8S_VERSION) --config $(KIND_CONFIG); \
	fi

.PHONY: kind-delete
kind-delete: kind ## Create kubernetes cluster using Kind.
	@if $(KIND) get clusters | grep -q $(KIND_CLUSTER_NAME); then \
		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
	fi

.PHONY: kind-prepare
kind-prepare: kind-create kind-load kube-ovn kube-virt ## Prepare kind cluster for kube-vim installation
	kubectl delete --ignore-not-found sc standard
	kubectl delete --ignore-not-found -n local-path-storage deploy local-path-provisioner
	kubectl config use-context kind-$(KIND_CLUSTER_NAME)
	@$(MAKE) kind-untaint-control-plane
	@echo "Installing kube-ovn to the kind"
	cd bin/kube-ovn; sed 's/VERSION=.*/VERSION=$(KUBE_OVN_VERSION)/' $(KUBE_OVN_INSTALL) | bash
	@echo "Installing kube-virt to the kind"
	kubectl create -f $(KUBE_VIRT_OPERATOR)
	kubectl create -f $(KUBE_VIRT_CR)
	kubectl -n kubevirt wait kv kubevirt --for condition=Available

.PHONY: kind-install
kind-install: kind-prepare ## Install kube-vim into prepared kind cluster
	kubectl create -f dist/manifests/kubevim.yaml
	@if [ "$(DEV)" = "1" ]; then \
		kubectl create -f dist/manifests/dev/nodeport.yaml; \
	fi


.PHONY: kind-untaint-control-plane
kind-untaint-control-plane:
	@for node in $(shell kubectl get no -o jsonpath='{.items[*].metadata.name}'); do \
		for key in $(CONTROL_PLANE_TAINTS); do \
			taint=$$(kubectl get no $$node -o jsonpath="{.spec.taints[?(@.key==\"$$key\")]}"); \
			if [ -n "$$taint" ]; then \
				kubectl taint node $$node $$key:NoSchedule-; \
			fi; \
		done; \
	done
