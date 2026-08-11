# syntax=docker/dockerfile:1

# VARIANT picks the distroless tag used by the runtime stage below. It must be
# declared here, before the first FROM, to be a global ARG — an ARG declared
# inside a build stage's scope (i.e. after that stage's FROM) does not carry
# over into a later stage's FROM, which is what caused it to resolve empty.
ARG VARIANT=debug

# ---- Build stage -----------------------------------------------------------
# The whole repo is copied so the root module's `replace` directives resolve the
# sibling modules (engine/, plugins/*, model/, store/, configgen/) from disk.
# Pin the build stage to the builder's native arch (BUILDPLATFORM) and cross-
# compile to the requested target arch below. Because the binary is pure Go
# (CGO_ENABLED=0), this is a fast native compile for every target — no QEMU
# emulation of the compiler (docs/repo-layout.md "Docker build notes").
FROM --platform=$BUILDPLATFORM golang:1.26 AS build

# Provided by buildx for the target platform of each manifest entry.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Copy only the go.mod/go.sum of the root module and every module pulled in via
# its `replace` directives, and prime the module cache, before copying any
# other source. Docker/BuildKit caches this layer by the content of just these
# files, so editing application code elsewhere in the repo doesn't bust it —
# `go mod download` only fetches external deps (the local `replace` targets
# aren't downloaded, just resolved from the go.mod files copied here).
COPY go.mod go.sum ./
COPY mdnssd/go.mod mdnssd/go.sum mdnssd/
COPY model/go.mod model/
COPY pluginreg/go.mod pluginreg/
COPY plugins/localrecords/go.mod plugins/localrecords/go.sum plugins/localrecords/
COPY plugins/mdnsbridge/go.mod plugins/mdnsbridge/go.sum plugins/mdnsbridge/
COPY plugins/querylog/go.mod plugins/querylog/go.sum plugins/querylog/
COPY plugins/vlancache/go.mod plugins/vlancache/go.sum plugins/vlancache/
COPY vlanmatch/go.mod vlanmatch/

# Cache Go modules and build output across runs
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# Version metadata for internal/version, passed in by CI (see
# .github/workflows/release.yml); a local `docker build` with no build-args
# falls back to the same "v0.0.0-dev"/"HEAD"/"unknown" defaults as `go build`.
ARG VERSION=v0.0.0-dev
ARG GIT_COMMIT=HEAD
ARG BUILD_DATE=unknown

# The root is the entrypoint module; its go.mod `replace` directives pull in
# engine/, plugins/*, model/, store/, configgen/ from disk. go.work is excluded
# from the build context (.dockerignore) so this is a plain-module build.
#
# Both cache mounts persist across builds independent of layer invalidation
# (unlike a COPY-based cache, which is invalidated whenever COPY . . above
# picks up any source change): /go/pkg/mod holds downloaded module sources,
# and /root/.cache/go-build holds compiled package objects, so unchanged
# vendor/dependency packages don't get recompiled just because application
# code changed.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w \
      -X github.com/mallardduck/BrambleGate/internal/version.Version=${VERSION} \
      -X github.com/mallardduck/BrambleGate/internal/version.GitCommit=${GIT_COMMIT} \
      -X github.com/mallardduck/BrambleGate/internal/version.Date=${BUILD_DATE}" \
    -o /out/bramblegate .

# ---- Runtime stage ---------------------------------------------------------
# distroless static: no package manager, just the binary + CA certs (CA certs
# are needed for ACME). Trivially cross-compilable to arm64.
#
# VARIANT (declared globally above): "debug" (default) layers in a busybox
# shell for local dev and prerelease images so `docker exec` isn't a dead
# end; "latest" is the true minimal/secure image with no shell at all and is
# what real (non-prerelease) release builds pass explicitly via
# --build-arg VARIANT=latest (see .github/workflows/release.yml).
ARG VARIANT
FROM gcr.io/distroless/static-debian12:${VARIANT}

# Runs as root (uid 0), deliberately: this is a DNS front door that binds the
# privileged port 53 and must seed/rewrite settings.yaml + records.yaml on a
# freshly bind-mounted /config — both of which a fresh, unconfigured host makes
# awkward for a nonroot uid (no CAP_NET_BIND_SERVICE, /config owned by host
# root). This matches how Pi-hole/dnsmasq homelab images ship. Operators who want
# to harden can run nonroot: chown the config volume to their uid, grant
# CAP_NET_BIND_SERVICE (or use high ports), and add `user:`—see the README.

COPY --from=build /out/bramblegate /usr/local/bin/bramblegate

# Config volume (settings.yaml, records.yaml, certs, .runtime) and the GUI port.
# DNS listener ports (53/853/443/…) are configured at runtime via
# settings.yaml. EXPOSE is documentation only (Docker still requires an
# explicit -p/ports: to actually publish a port) — DoH (443/tcp), DoH3
# (443/udp), and DoQ (853/udp) are omitted here since they default to off;
# add them yourself if you enable those listeners (see
# docs/deploying-docker.md).
VOLUME ["/config"]
EXPOSE 53/udp 53/tcp 853/tcp 8080/tcp

ENTRYPOINT ["/usr/local/bin/bramblegate"]