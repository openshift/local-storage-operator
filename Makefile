all build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o diskmaker ./cmd/diskmaker
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o local-storage-operator ./cmd/local-storage-operator
.PHONY: all build

clean:
	rm -f diskmaker local-storage-operator
.PHONY: clean
