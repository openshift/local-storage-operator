ifeq ($(REGISTRY),)
	REGISTRY = quay.io/gnufied/
endif

ifeq ($(VERSION),)
	VERSION = latest
endif

IMAGE = $(REGISTRY)local-volume-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)local-volume-provisioner:latest
DISKMAKER_IMAGE = $(REGISTRY)local-diskmaker:latest
OPERATOR_IMAGE= $(REGISTRY)local-storage-operator:0.0.9

all build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o diskmaker ./cmd/diskmaker
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o local-storage-operator ./cmd/local-storage-operator
.PHONY: all build

diskmaker-container:
	docker build -t $(DISKMAKER_IMAGE) -f Dockerfile.diskmaker
.PHONY: diskmaker-container

operator-container:
	docker build -t $(OPERATOR_IMAGE) -f Dockerfile
.PHONY: operator-container

clean:
	rm -f diskmaker local-storage-operator
.PHONY: clean
