VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt tidy check run-server css dist app standalone docker release

build:
	go build -ldflags '$(LDFLAGS)' -o bin/replicant ./cmd/replicant
	go build -ldflags '$(LDFLAGS)' -o bin/replicant-server ./cmd/replicant-server

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
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/replicant-server-linux-amd64 ./cmd/replicant-server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/replicant-server-linux-arm64 ./cmd/replicant-server
	cp deploy/replicant-server.service dist/
	@ls -la dist/

# macOS menu bar app with the replicant CLI bundled inside (dist/Replicant.app).
app:
	./macos/build-app.sh

# Same app with replicant-server bundled too: runs its own catalog on the Mac.
standalone:
	./macos/build-app.sh --standalone

# Server image for this machine's architecture (CI publishes multi-arch to GHCR).
docker:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/fenixstarlord/replicant:dev .

# Tag and push a release; GitHub Actions builds the apps and binaries.
# Usage: make release V=v0.1.0
release:
	@test -n "$(V)" || (echo "usage: make release V=vX.Y.Z" && exit 1)
	git tag -a $(V) -m "Replicant $(V)" && git push origin $(V)
