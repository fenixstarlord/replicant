# replicant-server image: a static Go binary on Debian slim with ffmpeg, so the
# server can extract metadata from folders mounted into the container.
# Multi-arch (amd64 + arm64); built and published by .github/workflows/docker.yml.

ARG GO_VERSION=1.27

# Base images come from Amazon's public mirror of the Docker official images
# (no Docker Hub auth or rate limits in CI; Docker Hub outages took builds down).
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
# Dependencies first so they cache separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/replicant-server ./cmd/replicant-server

FROM public.ecr.aws/docker/library/debian:bookworm AS runtime
# ffmpeg supplies ffprobe. Vendor tools (art-cmd, REDline) are not
# redistributable; bind-mount them under /opt/replicant-tools (on PATH).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 replicant && useradd --uid 1000 --gid replicant --home /data --no-create-home replicant \
    && mkdir -p /data /media /opt/replicant-tools && chown replicant:replicant /data
COPY --from=builder /out/replicant-server /usr/local/bin/replicant-server
ENV REPLICANT_DATA_DIR=/data \
    REPLICANT_LISTEN=:8080 \
    PATH=/opt/replicant-tools:/opt/replicant-tools/bin:$PATH
USER replicant
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["replicant-server", "healthz"]
ENTRYPOINT ["replicant-server"]
CMD ["serve"]
