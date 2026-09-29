# DEYROUTE build entry points. All builds are static (CGO_ENABLED=0).

BINARY   := deyroute
PKG      := github.com/localroot4/deyroute
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
GOFLAGS  := -trimpath
DIST     := dist
export GOTOOLCHAIN ?= local

COVER_PKGS := ./internal/config/... ./internal/failover/... ./internal/ports/... ./internal/errors/... ./internal/backend/...

.PHONY: all build build-all test race cover lint fmt vet docs check-docs integration clean install-local help

all: lint test build

help: ## show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

build: ## build ./dist/deyroute for the host arch
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/deyroute

build-all: ## build linux/amd64 and linux/arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_linux_amd64 ./cmd/deyroute
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_linux_arm64 ./cmd/deyroute
	@for f in $(DIST)/$(BINARY)_linux_*; do \
		size=$$(stat -c %s $$f); \
		if [ $$size -gt 31457280 ]; then echo "$$f is $$size bytes (> 30MB budget)"; exit 1; fi; \
		echo "$$f $$size bytes"; \
	done

test: ## unit tests
	go test ./...

race: ## unit tests with the race detector
	CGO_ENABLED=1 go test -race ./...

cover: ## coverage for the packages with a 70% floor (section 15)
	go test -coverprofile=$(DIST)/cover.out $(COVER_PKGS)
	@go run ./scripts/coverfloor -profile $(DIST)/cover.out -min 70

lint: ## golangci-lint + gofmt check
	@test -z "$$(gofmt -l . | grep -v '^dist/')" || (gofmt -l . ; echo "gofmt needed"; exit 1)
	golangci-lint run ./...

fmt: ## format sources
	gofmt -w $$(git ls-files '*.go')

vet:
	go vet ./...

docs: ## regenerate generated docs (ERRORS.md)
	go test ./internal/errors -run TestErrorsDocUpToDate -update

check-docs:
	go test ./internal/errors -run TestErrorsDocUpToDate

integration: build ## docker-compose scenarios (needs docker with systemd-capable containers)
	cd test/integration && ./run.sh

install-local: build ## install the host build to /usr/local/bin (root)
	install -m 0755 $(DIST)/$(BINARY) /usr/local/bin/$(BINARY).new && mv /usr/local/bin/$(BINARY).new /usr/local/bin/$(BINARY)
	ln -sf /usr/local/bin/$(BINARY) /usr/local/bin/dey

clean:
	rm -rf $(DIST)
