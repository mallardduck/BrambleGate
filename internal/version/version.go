// Package version holds the build-time version metadata for the bramblegate
// binary. The zero values below are what a plain `go build` produces; release
// builds (goreleaser and the Dockerfile) override them via -ldflags -X.
package version

import "strings"

var (
	// Version is the released tag (e.g. "v1.2.3"), "dev-<full-sha>" for a
	// rolling dev-image build (.github/workflows/dev-image.yml), or
	// "v0.0.0-dev" for a build that didn't go through goreleaser/Docker at
	// all.
	Version = "v0.0.0-dev"
	// GitCommit is the full commit SHA the binary was built from ("HEAD" for
	// a plain `go build` with no -ldflags). Display always shortens it via
	// ShortCommit — kept full here so CommitURL links to a real GitHub page.
	GitCommit = "HEAD"
	// Date is the build timestamp (RFC3339/UTC), set by the release pipeline.
	Date = "unknown"
)

// repoURL is BrambleGate's GitHub repository — the same host/path the module
// path (github.com/mallardduck/BrambleGate) resolves to.
const repoURL = "https://github.com/mallardduck/BrambleGate"

// DocsURL is the published user-facing documentation site (distinct from
// dev-docs/, this repo's internal design-doc set).
const DocsURL = "https://mallardduck.github.io/BrambleGate/"

// shortSHALen mirrors GitHub's own short-SHA display convention.
const shortSHALen = 7

// shortSHA truncates a full commit SHA for display. Left alone if it's
// already shorter (e.g. the "HEAD" placeholder).
func shortSHA(sha string) string {
	if len(sha) <= shortSHALen {
		return sha
	}
	return sha[:shortSHALen]
}

// ShortCommit is GitCommit truncated to GitHub's short-SHA length, for
// display (e.g. the GUI footer).
func ShortCommit() string {
	return shortSHA(GitCommit)
}

// String renders the version info for display, e.g. in the GUI footer:
// "v1.2.3 (abcdef1, 2026-08-09T00:00:00Z)" for a tagged release, or
// "dev-abcdef1 (abcdef1, ...)" for a dev-image build — both commit hashes
// shortened, even though the embedded one in a "dev-<sha>" Version is
// otherwise the full SHA (see ReleaseURL/CommitURL, which need the full
// value to link correctly).
func String() string {
	v := Version
	if sha, ok := strings.CutPrefix(v, "dev-"); ok {
		v = "dev-" + shortSHA(sha)
	}
	return v + " (" + ShortCommit() + ", " + Date + ")"
}

// CommitURL links to the exact commit this binary was built from, or the
// bare repo when no real commit is known (GitCommit is still the "HEAD"
// placeholder — a plain `go build` with no -ldflags).
func CommitURL() string {
	if GitCommit == "" || GitCommit == "HEAD" {
		return repoURL
	}
	return repoURL + "/commit/" + GitCommit
}

// ReleaseURL links to wherever this build's image/binary can be found:
// the bare repo when no real commit is known, the exact commit for a
// rolling dev-image build (Version "dev-<sha>" — there is no GitHub release
// for it), or the tagged GitHub release page for a real versioned build.
func ReleaseURL() string {
	if GitCommit == "" || GitCommit == "HEAD" {
		return repoURL
	}
	if strings.HasPrefix(Version, "dev-") {
		return CommitURL()
	}
	return repoURL + "/releases/tag/" + Version
}
