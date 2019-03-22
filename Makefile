ifeq ($(REGISTRY),)
	REGISTRY = quay.io/gnufied/
endif

ifeq ($(VERSION),)
	VERSION = latest
endif

IMAGE = $(REGISTRY)local-volume-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)local-volume-provisioner:latest
DISKMAKER_IMAGE = $(REGISTRY)local-diskmaker:latest

diskmaker-container:
	docker build -t $(DISKMAKER_IMAGE) -f Dockerfile.diskmaker
.PHONY: diskmaker-container

all build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o diskmaker ./cmd/diskmaker
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o local-storage-operator ./cmd/local-storage-operator
.PHONY: all build

clean:
	rm -f diskmaker local-storage-operator
.PHONY: clean
