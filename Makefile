VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt tidy check run-server css

build:
	go build -ldflags '$(LDFLAGS)' -o bin/shelf ./cmd/shelf
	go build -ldflags '$(LDFLAGS)' -o bin/shelf-server ./cmd/shelf-server

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -l -w .

tidy:
	go mod tidy

check: fmt vet test

run-server:
	./start.sh

# Placeholder until Phase 5: builds the embedded stylesheet with the
# standalone Tailwind CLI (see docs/design/m8-theme.md).
css:
	@echo "css build arrives in Phase 5" && exit 1
