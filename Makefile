ifeq ($(REGISTRY),)
	REGISTRY = quay.io/gnufied/
endif

ifeq ($(VERSION),)
	VERSION = latest
endif

CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/_output/bin
IMAGE = $(REGISTRY)local-volume-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)local-volume-provisioner:$(VERSION)
DISKMAKER_IMAGE = $(REGISTRY)local-diskmaker:$(VERSION)
OPERATOR_IMAGE= $(REGISTRY)local-storage-operator:$(VERSION)
REV=$(shell git describe --long --tags --match='v*' --dirty 2>/dev/null || git rev-list -n1 HEAD)

all build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o $(TARGET_DIR)/diskmaker ./cmd/diskmaker
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o $(TARGET_DIR)/local-storage-operator ./cmd/local-storage-operator
.PHONY: all build

images: diskmaker-container operator-container

push: images push-images

push-images:
	docker push ${DISKMAKER_IMAGE}
	docker push ${OPERATOR_IMAGE}

diskmaker-container:
	docker build --no-cache -t $(DISKMAKER_IMAGE) -f Dockerfile.diskmaker .
.PHONY: diskmaker-container

operator-container:
	docker build --no-cache -t $(OPERATOR_IMAGE) -f Dockerfile .
.PHONY: operator-container

clean:
	rm -f diskmaker local-storage-operator
.PHONY: clean

test:
	go test ./pkg/... ./cmd/... -coverprofile cover.out

test_e2e:
	./hack/test-e2e.sh
.PHONY: test
