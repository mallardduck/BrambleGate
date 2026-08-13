package version

import "testing"

// withVersion sets Version/GitCommit for the duration of a test, then
// restores them.
func withVersion(t *testing.T, v, sha string) {
	t.Helper()
	origV, origSHA := Version, GitCommit
	Version, GitCommit = v, sha
	t.Cleanup(func() { Version, GitCommit = origV, origSHA })
}

func TestStringShortensBothCommitOccurrences(t *testing.T) {
	withVersion(t, "dev-0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567")
	got := String()
	want := "dev-0123456 (0123456, unknown)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringForTaggedRelease(t *testing.T) {
	withVersion(t, "v1.2.3", "0123456789abcdef0123456789abcdef01234567")
	got := String()
	want := "v1.2.3 (0123456, unknown)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestReleaseURLNoRealCommitFallsBackToRepo(t *testing.T) {
	withVersion(t, "v0.0.0-dev", "HEAD")
	if got := ReleaseURL(); got != repoURL {
		t.Fatalf("ReleaseURL() = %q, want the bare repo URL %q", got, repoURL)
	}
	if got := CommitURL(); got != repoURL {
		t.Fatalf("CommitURL() = %q, want the bare repo URL %q", got, repoURL)
	}
}

func TestReleaseURLDevImageLinksToCommit(t *testing.T) {
	withVersion(t, "dev-0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567")
	want := repoURL + "/commit/0123456789abcdef0123456789abcdef01234567"
	if got := ReleaseURL(); got != want {
		t.Fatalf("ReleaseURL() = %q, want %q", got, want)
	}
}

func TestReleaseURLTaggedReleaseLinksToReleasePage(t *testing.T) {
	withVersion(t, "v1.2.3", "0123456789abcdef0123456789abcdef01234567")
	want := repoURL + "/releases/tag/v1.2.3"
	if got := ReleaseURL(); got != want {
		t.Fatalf("ReleaseURL() = %q, want %q", got, want)
	}
}
