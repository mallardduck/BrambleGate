// Package version holds the build-time version metadata for the bramblegate
// binary. The zero values below are what a plain `go build` produces; release
// builds (goreleaser and the Dockerfile) override them via -ldflags -X.
package version

var (
	// Version is the released tag (e.g. "v1.2.3"), or "v0.0.0-dev" for a
	// build that didn't go through goreleaser/Docker.
	Version = "v0.0.0-dev"
	// GitCommit is the short commit SHA the binary was built from.
	GitCommit = "HEAD"
	// Date is the build timestamp (RFC3339/UTC), set by the release pipeline.
	Date = "unknown"
)

// String renders the version info for display, e.g. in the GUI footer:
// "v1.2.3 (abcdef1, 2026-08-09T00:00:00Z)".
func String() string {
	return Version + " (" + GitCommit + ", " + Date + ")"
}
