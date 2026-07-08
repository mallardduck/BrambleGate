# syntax=docker/dockerfile:1

# ---- Build stage -----------------------------------------------------------
# The whole repo is copied so the root module's `replace` directives resolve the
# sibling modules (engine/, plugins/*, model/, store/, configgen/) from disk.
# Pin the build stage to the builder's native arch (BUILDPLATFORM) and cross-
# compile to the requested target arch below. Because the binary is pure Go
# (CGO_ENABLED=0), this is a fast native compile for every target — no QEMU
# emulation of the compiler (docs/repo-layout.md "Docker build notes").
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

# Provided by buildx for the target platform of each manifest entry.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY . .

# The root is the entrypoint module; its go.mod `replace` directives pull in
# engine/, plugins/*, model/, store/, configgen/ from disk. go.work is excluded
# from the build context (.dockerignore) so this is a plain-module build.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' -o /out/bramblegate .

# ---- Runtime stage ---------------------------------------------------------
# distroless static: no shell, no package manager, just the binary + CA certs
# (CA certs are needed for ACME). Trivially cross-compilable to arm64.
#
# Runs as root (uid 0), deliberately: this is a DNS front door that binds the
# privileged port 53 and must seed/rewrite settings.yaml + records.yaml on a
# freshly bind-mounted /config — both of which a fresh, unconfigured host makes
# awkward for a nonroot uid (no CAP_NET_BIND_SERVICE, /config owned by host
# root). This matches how Pi-hole/dnsmasq homelab images ship. Operators who want
# to harden can run nonroot: chown the config volume to their uid, grant
# CAP_NET_BIND_SERVICE (or use high ports), and add `user:`—see the README.
FROM gcr.io/distroless/static-debian12:latest

COPY --from=build /out/bramblegate /usr/local/bin/bramblegate

# Config volume (settings.yaml, records.yaml, certs, .runtime) and the GUI port.
# DNS listener ports (53/853/…) are configured at runtime via settings.yaml.
VOLUME ["/config"]
EXPOSE 53/udp 53/tcp 853 8080

ENTRYPOINT ["/usr/local/bin/bramblegate"]