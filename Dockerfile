# shelf-server image: a static Go binary on Debian slim with ffmpeg, so the
# server can extract metadata from folders mounted into the container.
# Multi-arch (amd64 + arm64); built and published by .github/workflows/docker.yml.

ARG GO_VERSION=1.27

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
# Dependencies first so they cache separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/shelf-server ./cmd/shelf-server

FROM debian:bookworm-slim AS runtime
# ffmpeg supplies ffprobe. Vendor tools (art-cmd, REDline) are not
# redistributable; bind-mount them under /opt/shelf-tools (on PATH).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 shelf && useradd --uid 1000 --gid shelf --home /data --no-create-home shelf \
    && mkdir -p /data /media /opt/shelf-tools && chown shelf:shelf /data
COPY --from=builder /out/shelf-server /usr/local/bin/shelf-server
ENV SHELF_DATA_DIR=/data \
    SHELF_LISTEN=:8080 \
    PATH=/opt/shelf-tools:/opt/shelf-tools/bin:$PATH
USER shelf
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["shelf-server", "healthz"]
ENTRYPOINT ["shelf-server"]
CMD ["serve"]
