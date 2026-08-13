# syntax=docker/dockerfile:1

# acceptance/ is its own Go module with no sibling-module `replace` directives
# (dev-docs/repo-layout.md) — the build context is this directory alone, not
# the repo root, so nothing outside it needs to be copied in.

FROM golang:1.26 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bg-acceptance .

# distroless static: CA certs (for the TLS/HTTPS checks) with no shell/package
# manager. nonroot — this tool only ever makes outbound DNS/TLS/HTTP calls, no
# privileged port binding, unlike the main bramblegate image.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/bg-acceptance /usr/local/bin/bg-acceptance

ENTRYPOINT ["/usr/local/bin/bg-acceptance"]
CMD ["--help"]
