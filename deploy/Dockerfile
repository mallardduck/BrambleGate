# syntax=docker/dockerfile:1

# ---- Build stage -----------------------------------------------------------
# Multi-module workspace: the whole repo is copied so go.work can resolve the
# sibling modules that cmd/brambledns depends on. The frontend build stage and
# go:embed of static assets arrive in a later phase (docs/repo-layout.md).
FROM golang:1.25 AS build

WORKDIR /src
COPY . .

# The root is the entrypoint module; go.work ties in engine/, plugins/*, model/,
# store/, configgen/.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/brambledns .

# ---- Runtime stage ---------------------------------------------------------
# distroless static: no shell, no package manager, just the binary + CA certs
# (needed later for ACME). Trivially cross-compilable to arm64.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/brambledns /usr/local/bin/brambledns

# GUI port (engine listeners are configured at runtime via settings.yaml).
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/brambledns"]