VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt tidy check run-server css dist app standalone docker

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

css:
	./build-css.sh

# Static linux/amd64 server binary for the home server, plus the systemd unit.
dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/shelf-server-linux-amd64 ./cmd/shelf-server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/shelf-server-linux-arm64 ./cmd/shelf-server
	cp deploy/shelf-server.service dist/
	@ls -la dist/

# macOS menu bar app with the shelf CLI bundled inside (dist/Shelf.app).
app:
	./macos/build-app.sh

# Same app with shelf-server bundled too: runs its own catalog on the Mac.
standalone:
	./macos/build-app.sh --standalone

# Server image for this machine's architecture (CI publishes multi-arch to GHCR).
docker:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/fenixstarlord/indexserver:dev .
