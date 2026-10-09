VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt tidy check run-server css dist app

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

# Static linux/amd64 server binary for the home server, plus the systemd unit.
dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/shelf-server-linux-amd64 ./cmd/shelf-server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/shelf-server-linux-arm64 ./cmd/shelf-server
	cp deploy/shelf-server.service dist/
	@ls -la dist/

# macOS menu bar app with the shelf CLI bundled inside (dist/Shelf.app).
app:
	./macos/build-app.sh
