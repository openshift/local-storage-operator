# To generate a bundle for a specific REGISTRY, REPO, and VERSION, you can:
# make bundle REGISTRY=quay.io/username REPO=lso VERSION=latest
ifeq ($(REGISTRY),)
	REGISTRY = quay.io/openshift
endif
ifeq ($(REPO),)
	REPO = local-storage-operator
endif
ifeq ($(VERSION),)
	VERSION = latest
endif

# Use podman or docker to build containers. Can bet set explicitly.
# make bundle REGISTRY=quay.io/username TOOL_BIN=`which docker`
ifeq ($(TOOL_BIN),)
	TOOL_BIN=$(shell which podman 2>/dev/null || which docker 2>/dev/null)
endif

TARGET_GOOS=$(shell go env GOOS)
TARGET_GOARCH=$(shell go env GOARCH)

CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/_output/bin
OPERATOR_IMAGE= $(REGISTRY)/$(REPO):operator-$(VERSION)
DISKMAKER_IMAGE = $(REGISTRY)/$(REPO):diskmaker-$(VERSION)
MUSTGATHER_IMAGE = $(REGISTRY)/$(REPO):mustgather-$(VERSION)
BUNDLE_IMAGE = $(REGISTRY)/$(REPO):bundle-$(VERSION)
INDEX_IMAGE = $(REGISTRY)/$(REPO):index-$(VERSION)
REV=$(shell git describe --long --tags --match='v*' --dirty 2>/dev/null || git rev-list -n1 HEAD)
BIN_PATH=$(CURPATH)/bin
YQ = $(BIN_PATH)/yq
YQ_VERSION = v4.47.1
export PATH := $(BIN_PATH):$(PATH)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
        targets/openshift/yq.mk \
)

update: metadata manifests generate fmt
.PHONY: update

verify: vet
	./hack/verify-metadata.sh
	./hack/verify-manifests.sh
	./hack/verify-generate.sh
	./hack/verify-gofmt.sh
.PHONY: verify

# Bump OCP version in CSV and OLM metadata
#
# Example:
#   make metadata OCP_VERSION=4.20.0
metadata: ensure-yq
ifdef OCP_VERSION
	./hack/update-metadata.sh $(OCP_VERSION)
else
	./hack/update-metadata.sh
endif
.PHONY: metadata

manifests: controller-gen ## Generate CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=local-storage-operator crd paths="./api/..." output:artifacts:config=config/manifests/stable
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
	$(call go-get-tool,$(ENVTEST_ASSETS_DIR),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)
	go test ./pkg/... -coverprofile cover.out
.PHONY: test

CONTROLLER_GEN = $(BIN_PATH)/controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0)

clean-controller-gen:
	rm -f $(CONTROLLER_GEN)
.PHONY: clean-controller-gen

# go-get-tool will 'go get' any package $2 and install it to $1.
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(shell dirname $(1)) GOFLAGS="" go install $(2) ;\
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

images: diskmaker-container operator-container
.PHONY: images

push: images
	$(TOOL_BIN) push $(OPERATOR_IMAGE)
	$(TOOL_BIN) push $(DISKMAKER_IMAGE)
.PHONY: push

must-gather:
	$(TOOL_BIN) build -t $(MUSTGATHER_IMAGE) -f $(CURPATH)/Dockerfile.mustgather .
.PHONY: must-gather

# this is ugly, but allows us to build dev containers without tripping over yum install
diskmaker-dockerfile-hack:
	sed -e 's~registry.ci.openshift.org/ocp/.*:base.*~almalinux:9~' Dockerfile.diskmaker.rhel7 > Dockerfile.diskmaker.hack
.PHONY: diskmaker-dockerfile-hack

diskmaker-container: diskmaker-dockerfile-hack
	$(TOOL_BIN) build -t $(DISKMAKER_IMAGE) -f $(CURPATH)/Dockerfile.diskmaker.hack .
.PHONY: diskmaker-container

operator-container:
	$(TOOL_BIN) build -t $(OPERATOR_IMAGE) -f $(CURPATH)/Dockerfile.rhel7 .
.PHONY: operator-container

bundle: push
	./hack/create-bundle.sh $(OPERATOR_IMAGE) $(DISKMAKER_IMAGE) $(BUNDLE_IMAGE) $(INDEX_IMAGE)
.PHONY: bundle

clean: clean-controller-gen clean-yq
	rm -f $(TARGET_DIR)/diskmaker $(TARGET_DIR)/local-storage-operator
.PHONY: clean

test_e2e:
	./hack/test-e2e.sh
.PHONY: test_e2e
