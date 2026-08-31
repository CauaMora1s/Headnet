# syntax=docker/dockerfile:1

# Headnet control-plane image.
#
# The result is a distroless, non-root, statically linked image. The control
# plane holds device public keys and, from Phase 1, password hashes; it has no
# business running as root or shipping a shell an attacker could use after a
# compromise.
#
# Note: the web UI is not built into this image, and the server does not serve
# static files.
#
# An earlier version of this comment said embedding would arrive with the
# administrative screens in Phase 1. The screens exist now; the embedding does
# not, and is deferred to Phase 2 alongside the client daemon. Until then the
# UI has to be served separately — by the Vite dev server, or as static files
# behind the same reverse proxy that fronts this container, so that it shares
# an origin with the API and its session cookie works.
#
# See docs/deployment.md.

# --- Stage 1: build the binaries --------------------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src

# Static binaries: the SQLite driver is pure Go precisely so cgo can stay off,
# which is what allows the distroless base below.
ENV CGO_ENABLED=0 GOOS=linux

# Copy the manifests first so the dependency layer is cached independently of
# the source, which is what keeps rebuilds fast.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETARCH

RUN VERSION_PKG=github.com/CauaMora1s/Headnet/internal/version && \
    LDFLAGS="-s -w -X ${VERSION_PKG}.version=${VERSION} -X ${VERSION_PKG}.commit=${COMMIT} -X ${VERSION_PKG}.buildDate=${BUILD_DATE}" && \
    GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "${LDFLAGS}" -o /out/headnet-server ./apps/server && \
    GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "${LDFLAGS}" -o /out/headnet ./apps/cli

# --- Stage 2: runtime --------------------------------------------------------
# Distroless static: no shell, no package manager, no libc. There is nothing
# for an attacker who achieves code execution to pivot into.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/headnet-server /usr/local/bin/headnet-server
COPY --from=build /out/headnet /usr/local/bin/headnet

# The SQLite database lives here. Mount a volume over it, or the whole control
# plane is lost when the container is replaced.
WORKDIR /data
VOLUME ["/data"]

# 65532 is the distroless "nonroot" user.
USER 65532:65532

ENV HEADNET_SERVER_LISTEN=:8080 \
    HEADNET_DATABASE_DRIVER=sqlite \
    HEADNET_DATABASE_PATH=/data/headnet.db \
    HEADNET_LOG_FORMAT=json

EXPOSE 8080

# The healthcheck uses the shipped CLI rather than curl, which this image
# deliberately does not contain.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/headnet", "diagnostics", "-server", "http://127.0.0.1:8080", "-timeout", "4s"]

ENTRYPOINT ["/usr/local/bin/headnet-server"]
