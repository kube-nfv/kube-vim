NAME ?= kube-vim


IMG_BASE ?= ghcr.io/kube-nfv
KUBEVIM = kube-vim
KUBEVIM_VERSION ?= latest
KUBEVIM_IMG ?= $(IMG_BASE)/$(KUBEVIM):$(KUBEVIM_VERSION)

KUBEVIM_GATEWAY = gateway
KUBEVIM_GATEWAY_VERSION ?= latest
KUBEVIM_GATEWAY_IMG ?= $(IMG_BASE)/$(KUBEVIM_GATEWAY):$(KUBEVIM_GATEWAY_VERSION)

DEV ?= 0

K8S_VERSION ?= v1.31.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif
LOCALBIN ?= $(shell pwd)/bin

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

.PHONY: generate
generate: ## Generate golang files
	go generate ./...

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
build: generate fmt vet $(LOCALBIN)/$(KUBEVIM) $(LOCALBIN)/$(KUBEVIM_GATEWAY)

$(LOCALBIN)/$(KUBEVIM): $(LOCALBIN) FORCE
	go build -o $@ cmd/kube-vim/main.go

$(LOCALBIN)/$(KUBEVIM_GATEWAY): $(LOCALBIN) FORCE
	go build -o $@ cmd/kube-vim-gateway/main.go

.PHONY: test
test: fmt vet
	go test -v -count=1 ./...

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: docker-kubevim-build docker-gateway-build ## Build kubevim related docker images.

.PHONY: docker-kubevim-build
docker-kubevim-build: ## Build kubevim docker image.
	$(CONTAINER_TOOL) build -t $(KUBEVIM_IMG) -f dist/Dockerfile.$(KUBEVIM) .

.PHONY: docker-gateway-build
docker-gateway-build: ## Build kubevim gateway docker image.
	$(CONTAINER_TOOL) build -t $(KUBEVIM_GATEWAY_IMG) -f dist/Dockerfile.$(KUBEVIM_GATEWAY) .

.PHONY: build-dist-manifests
build-dist-manifests:
	mkdir -p dist

##@ Dependencies

## Location to install dependencies to
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
# TODO: Replace with the kube-openapi generator
OAPI_CODEGEN ?= $(LOCALBIN)/oapi-codegen
KUBE_OVN_INSTALL ?= $(LOCALBIN)/kube-ovn/install.sh
KUBE_VIRT_OPERATOR ?= $(LOCALBIN)/kube-virt/kubevirt-operator.yaml
KUBE_VIRT_CR       ?= $(LOCALBIN)/kube-virt/kubevirt-cr.yaml
KUBE_VIRT_CDI_OPERATOR ?= $(LOCALBIN)/kube-virt-cdi/cdi-operator.yaml
KUBE_VIRT_CDI_CR       ?= $(LOCALBIN)/kube-virt-cdi/cdi-cr.yaml
MULTUS_CNI_THICK_DS ?= $(LOCALBIN)/multus-cni/multus-daemonset-thick.yml

CSI_SNAPSHOTTER_CRS_DIR  ?= $(LOCALBIN)/csi-snapshotter
CSI_SNAPSHOTTER_CR_NAMES ?= snapshot.storage.k8s.io_volumesnapshotclasses.yaml \
					   		snapshot.storage.k8s.io_volumesnapshotcontents.yaml \
					   		snapshot.storage.k8s.io_volumesnapshots.yaml
CSI_SNAPSHOTTER_CTRL_NAMES ?= rbac-snapshot-controller.yaml \
							  setup-snapshot-controller.yaml
CSI_HOSTPATH_DRIVER_INSTALL  ?= $(LOCALBIN)/csi-hostpath/deploy-hostpath.sh
CSI_HOSTPATH_DRIVER_DEPS_DIR ?= $(LOCALBIN)/csi-hostpath/hostpath
CSI_HOSTPATH_DRIVER_DEPS     ?= csi-hostpath-driverinfo.yaml \
								csi-hostpath-plugin.yaml \
								csi-hostpath-snapshotclass.yaml \
								csi-hostpath-testing.yaml


KIND_VERSION ?= v0.23.0
KIND_CLOUD_PROVIDER_VERSION ?= v0.4.0
YQ_VERSION ?= v4.44.1
GOLANGCI_LINT_VERSION ?= v1.59.1
OAPI_CODEGEN_VERSION ?= v2.4.0

CSI_SNAPSHOTTER_CR_VERSION ?= release-6.3
CSI_SNAPSHOTTER_CONTROLLER_VERSION ?= v6.3.3
CSI_HOSTPATH_DRIVER_VERSION ?= v1.15.0
KUBE_OVN_VERSION ?= v1.13.0
KUBE_OVN_RELEASE ?= 1.13
KUBE_VIRT_VERSION ?= v1.4.0
KUBE_VIRT_CDI_VERSION ?=v1.61.0
MULTUS_CNI_VERSION ?= v4.1.4

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

.PHONY: oapi-codegen
oapi-codegen: $(LOCALBIN)
	@test -x $(OAPI_CODEGEN) && $(OAPI_CODEGEN) --version | grep -q $(OAPI_CODEGEN_VERSION) || \
	GOBIN=$(LOCALBIN) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen$(OAPI_CODEGEN_VERSION)

.PHONY: csi-snapshotter
csi-snapshotter: $(LOCALBIN)
	@for snapshotter_cr in $(CSI_SNAPSHOTTER_CR_NAMES); do \
		CR_PATH="$(CSI_SNAPSHOTTER_CRS_DIR)/$$snapshotter_cr"; \
		test -e $$CR_PATH || \
		wget -P $(CSI_SNAPSHOTTER_CRS_DIR) https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/$(CSI_SNAPSHOTTER_CR_VERSION)/client/config/crd/$$snapshotter_cr; \
	done
	@for snapshotter_ctrl in $(CSI_SNAPSHOTTER_CTRL_NAMES); do \
		CTRL_PATH="$(CSI_SNAPSHOTTER_CRS_DIR)/$$snapshotter_ctrl"; \
		test -e $$CTRL_PATH || \
		wget -P $(CSI_SNAPSHOTTER_CRS_DIR) https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/$(CSI_SNAPSHOTTER_CONTROLLER_VERSION)/deploy/kubernetes/snapshot-controller/$$snapshotter_ctrl; \
	done

.PHONY: csi-host-path
csi-host-path: $(LOCALBIN)
	@test -x $(CSI_HOSTPATH_DRIVER_INSTALL) || \
	wget -P $(LOCALBIN)/csi-hostpath https://raw.githubusercontent.com/kubernetes-csi/csi-driver-host-path/$(CSI_HOSTPATH_DRIVER_VERSION)/deploy/util/deploy-hostpath.sh; \
	chmod +x $(CSI_HOSTPATH_DRIVER_INSTALL)
	@for dep in $(CSI_HOSTPATH_DRIVER_DEPS); do \
		wget -P $(LOCALBIN)/csi-hostpath/hostpath https://raw.githubusercontent.com/kubernetes-csi/csi-driver-host-path/$(CSI_HOSTPATH_DRIVER_VERSION)/deploy/kubernetes-1.27/hostpath/$$dep; \
	done

.PHONY: kube-ovn
kube-ovn: $(LOCALBIN)
	@test -x $(KUBE_OVN_INSTALL) || \
	wget -P $(LOCALBIN)/kube-ovn https://raw.githubusercontent.com/kubeovn/kube-ovn/release-$(KUBE_OVN_RELEASE)/dist/images/install.sh; chmod +x $(KUBE_OVN_INSTALL)

.PHONY: kube-virt
kube-virt: $(LOCALBIN)
	@test -e $(KUBE_VIRT_OPERATOR) || \
	wget -P $(LOCALBIN)/kube-virt https://github.com/kubevirt/kubevirt/releases/download/$(KUBE_VIRT_VERSION)/kubevirt-operator.yaml
	@test -e $(KUBE_VIRT_CR) || \
	wget -P $(LOCALBIN)/kube-virt https://github.com/kubevirt/kubevirt/releases/download/$(KUBE_VIRT_VERSION)/kubevirt-cr.yaml

.PHONY: kube-virt-cdi
kube-virt-cdi: $(LOCALBIN)
	@test -e $(KUBE_VIRT_CDI_OPERATOR) || \
	wget -P $(LOCALBIN)/kube-virt-cdi https://github.com/kubevirt/containerized-data-importer/releases/download/$(KUBE_VIRT_CDI_VERSION)/cdi-operator.yaml
	@test -e $(KUBE_VIRT_CDI_CR) || \
	wget -P $(LOCALBIN)/kube-virt-cdi https://github.com/kubevirt/containerized-data-importer/releases/download/$(KUBE_VIRT_CDI_VERSION)/cdi-cr.yaml

.PHONY: multus-cni
multus-cni: $(LOCALBIN)
	@test -e $(MULTUS_CNI_THICK_DS) || \
	wget -P $(LOCALBIN)/multus-cni https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/$(MULTUS_CNI_VERSION)/deployments/multus-daemonset-thick.yml

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
kind-load: kind-load-kubevim kind-load-gateway ## Upload docker images realted to the kubevim application to the local Kind cluster.

.PHONY: kind-load-kubevim
kind-load-kubevim: docker-kubevim-build kind kind-create ## Upload kubevim image to the local Kind cluster.
	$(KIND) load docker-image $(KUBEVIM_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: kind-load-gateway
kind-load-gateway: docker-gateway-build kind kind-create ## Upload kubevim gateway image to the local Kind cluster.
	$(KIND) load docker-image $(KUBEVIM_GATEWAY_IMG) --name $(KIND_CLUSTER_NAME)

.PHONY: kind-create
kind-create: kind yq ## Create kubernetes cluster using Kind.
	@if ! $(KIND) get clusters | grep -q $(KIND_CLUSTER_NAME); then \
		$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image kindest/node:$(K8S_VERSION) --config $(KIND_CONFIG); \
	elif ! $(CONTAINER_TOOL) container inspect $$($(KIND) get nodes --name $(KIND_CLUSTER_NAME)) | $(YQ) e '.[0].Config.Image' | grep -q $(K8S_VERSION); then \
  		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
		$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image kindest/node:$(K8S_VERSION) --config $(KIND_CONFIG); \
	fi

.PHONY: kind-delete
kind-delete: kind kind-delete-cloud-provider ## Create kubernetes cluster using Kind.
	@if $(KIND) get clusters | grep -q $(KIND_CLUSTER_NAME); then \
		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
	fi

.PHONY: kind-prepare
kind-prepare: kind-create kind-load kube-ovn kube-virt kube-virt-cdi multus-cni kind-prepare-cloud-provider ## Prepare kind cluster for kube-vim installation
	# delete default storage class
	kubectl delete --ignore-not-found sc standard
	kubectl delete --ignore-not-found -n local-path-storage deploy local-path-provisioner
	kubectl config use-context kind-$(KIND_CLUSTER_NAME)
	@$(MAKE) kind-untaint-control-plane
	@echo "Installing kube-ovn to the kind"
	cd bin/kube-ovn; sed 's/VERSION=.*/VERSION=$(KUBE_OVN_VERSION)/' $(KUBE_OVN_INSTALL) | bash
	@echo "Installing multus-cni to the kind"
	kubectl create -f $(MULTUS_CNI_THICK_DS)
	kubectl rollout status daemonset/kube-multus-ds -n kube-system
	@echo "Installing kube-virt to the kind"
	kubectl create -f $(KUBE_VIRT_OPERATOR)
	kubectl create -f $(KUBE_VIRT_CR)
	# kubectl -n kubevirt wait kv kubevirt --for condition=Available
	kubectl create -f $(KUBE_VIRT_CDI_OPERATOR)
	kubectl create -f $(KUBE_VIRT_CDI_CR)
	kubectl create -f dist/manifests/kubevim-lb.yaml


KIND_CLOUD_PROVIDER_CONTAINER_NAME ?= kind-cloud-provider
.PHONY: kind-prepare-cloud-provider
kind-prepare-cloud-provider:
	docker run -d --rm --network kind -v /var/run/docker.sock:/var/run/docker.sock \
		--name $(KIND_CLOUD_PROVIDER_CONTAINER_NAME) \
		registry.k8s.io/cloud-provider-kind/cloud-controller-manager:$(KIND_CLOUD_PROVIDER_VERSION)

.PHONY: kind-delete-cloud-provider
kind-delete-cloud-provider:
	@if docker ps -q --filter "name=$(KIND_CLOUD_PROVIDER_CONTAINER_NAME)" | grep -q .; then \
		docker rm -f $(KIND_CLOUD_PROVIDER_CONTAINER_NAME); \
	fi
	@for ccm in $(shell docker ps -q --filter "name=kindccm"); do \
		docker rm -f $$ccm; \
	done

.PHONY: kind-prepare-dev
kind-prepare-dev: ## Prepare development evironment for kube-vim operation
	kubectl create -f $(CSI_SNAPSHOTTER_CRS_DIR)
	bash $(CSI_HOSTPATH_DRIVER_INSTALL)
	kubectl create -f dist/manifests/dev/csi-storageclass.yaml

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

FORCE:
