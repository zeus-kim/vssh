VERSION := $(shell grep -E '^[[:space:]]*version[[:space:]]*=' cmd/vssh/main.go | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

.PHONY: all build clean install test test-python release checksums

all: build

build:
	go build $(LDFLAGS) -o vssh ./cmd/vssh

test:
	go test ./...
	PYTHONPATH=src python3 -m unittest discover -s tests

test-python:
	PYTHONPATH=src python3 -m unittest discover -s tests

clean:
	rm -rf vssh vssh-* dist

install: build
	sudo cp vssh /usr/local/bin/
	@echo "Installed to /usr/local/bin"

release:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/vssh-linux-amd64 ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/vssh-linux-arm64 ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o dist/vssh-linux-arm ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build $(LDFLAGS) -o dist/vssh-linux-386 ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build $(LDFLAGS) -o dist/vssh-linux-riscv64 ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build $(LDFLAGS) -o dist/vssh-linux-ppc64le ./cmd/vssh
	CGO_ENABLED=0 GOOS=linux GOARCH=s390x go build $(LDFLAGS) -o dist/vssh-linux-s390x ./cmd/vssh
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/vssh-darwin-amd64 ./cmd/vssh
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/vssh-darwin-arm64 ./cmd/vssh
	$(MAKE) checksums
	@ls -la dist

checksums:
	cd dist && shasum -a 256 vssh-linux-* vssh-darwin-* > checksums.txt
