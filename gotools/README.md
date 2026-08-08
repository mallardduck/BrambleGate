# gotools

Go-based dev tools (`task`, `goreleaser`, …), each pinned via
[`go tool`](https://tip.golang.org/doc/modules/managing-dependencies#tools) in
its own `go.mod` under `gotools/<name>/`. A separate go.mod per tool keeps a
tool's dependency graph (and its `go` version requirement) out of the root
module and out of every other tool — e.g. goreleaser requiring a newer `go`
directive than the app itself doesn't force that bump on `go.mod`.

Nothing needs to be installed system-wide: `go tool -modfile=...` builds and
caches the binary on first use.

## Using a tool

```sh
go tool -modfile=gotools/<name>/go.mod <name> [args...]
```

The Taskfile at the repo root wraps these for common workflows — see
`task --list-all` (or `go tool -modfile=gotools/task/go.mod task --list-all`).

## Adding a new tool

```sh
TOOL=<name>
mkdir -p gotools/"$TOOL"
GOWORK=off go mod init -modfile=gotools/"$TOOL"/go.mod github.com/mallardduck/BrambleGate/gotools/"$TOOL"
GOWORK=off go get -tool -modfile=gotools/"$TOOL"/go.mod <module>@<version>
```

## Updating a tool

```sh
GOWORK=off go get -tool -modfile=gotools/<name>/go.mod <module>@<new-version>
```