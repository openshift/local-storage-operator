# To generate a bundle for a specific REGISTRY and VERSION, you can:
# make bundle REGISTRY=quay.io/username VERSION=latest

ifeq ($(REGISTRY),)
	REGISTRY = quay.io/openshift/
endif

ifeq ($(VERSION),)
	VERSION = latest
endif

TARGET_GOOS=$(shell go env GOOS)
TARGET_GOARCH=$(shell go env GOARCH)

CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/_output/bin
IMAGE = $(REGISTRY)/local-volume-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)/local-volume-provisioner:$(VERSION)
DISKMAKER_IMAGE = $(REGISTRY)/local-diskmaker:$(VERSION)
OPERATOR_IMAGE= $(REGISTRY)/local-storage-operator:$(VERSION)
MUST_GATHER_IMAGE = $(REGISTRY)/local-must-gather:$(VERSION)
REV=$(shell git describe --long --tags --match='v*' --dirty 2>/dev/null || git rev-list -n1 HEAD)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

update: rbac manifests generate fmt
.PHONY: update

verify: vet
	./hack/verify-rbac.sh
	./hack/verify-manifests.sh
	./hack/verify-generate.sh
	./hack/verify-gofmt.sh
.PHONY: verify

rbac: controller-gen ## Generate ClusterRole and Role objects.
	$(CONTROLLER_GEN) rbac:roleName=local-storage-operator webhook paths="./pkg/controllers/..." output:artifacts:config=config/rbac
	$(CONTROLLER_GEN) rbac:roleName=local-storage-admin paths="./pkg/diskmaker/controllers/..."  output:artifacts:config=config/rbac/diskmaker
.PHONY: rbac

manifests: controller-gen ## Generate CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=local-storage-operator crd paths="./api/..." output:artifacts:config=config/crd/bases
.PHONY: manifests

generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
.PHONY: generate

fmt: ## Run go fmt against code.
	go fmt ./...
.PHONY: fmt

vet: ## Run go vet against code.
	go vet ./...
.PHONY: vet

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin

test: ## Run unit tests.
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/v0.7.2/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR)
	go test ./pkg/... -coverprofile cover.out
.PHONY: test

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.12.1)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin GOFLAGS="" go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

all build: build-diskmaker build-operator
.PHONY: all build

build-diskmaker:
	env GOOS=$(TARGET_GOOS) GOARCH=$(TARGET_GOARCH) go build -mod=vendor -ldflags '-X main.version=$(REV)' -o $(TARGET_DIR)/diskmaker $(CURPATH)/cmd/diskmaker-manager
.PHONY: build-diskmaker

build-operator:
	env GOOS=$(TARGET_GOOS) GOARCH=$(TARGET_GOARCH) go build -mod=vendor -ldflags '-X main.version=$(REV)' -o $(TARGET_DIR)/local-storage-operator $(CURPATH)/cmd/local-storage-operator
.PHONY: build-operator

images: diskmaker-container operator-container must-gather
.PHONY: images

must-gather:
	docker build --no-cache -t $(MUST_GATHER_IMAGE) -f $(CURPATH)/Dockerfile.mustgather .
.PHONY: must-gather

diskmaker-container:
	docker build --no-cache -t $(DISKMAKER_IMAGE) -f $(CURPATH)/Dockerfile.diskmaker.rhel7 .
.PHONY: diskmaker-container

operator-container:
	docker build --no-cache -t $(OPERATOR_IMAGE) -f $(CURPATH)/Dockerfile .
.PHONY: operator-container

bundle:
	./hack/sync_bundle
.PHONY: bundle

clean:
	rm -f $(TARGET_DIR)/diskmaker $(TARGET_DIR)/local-storage-operator
.PHONY: clean

test_e2e:
	./hack/test-e2e.sh
.PHONY: test_e2e
