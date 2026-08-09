package engine

import (
	"runtime"
	"testing"
)

// A minimal, dependency-free Corefile: whoami answers locally with no upstream,
// on a high loopback port so the test needs no privileges and hits no network.
func corefileOn(port string) []byte {
	return []byte(".:" + port + " {\n\tbind 127.0.0.1\n\twhoami\n}\n")
}

// CoreDNS double-binds the TCP listen socket on Windows (WSAEADDRINUSE) — a
// platform quirk of the caddy fork's startup, not a defect in this wrapper. The
// deployment target is Linux (Docker), where these pass; CI runs on Linux too.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("engine port-binding is verified on Linux/CI; CoreDNS double-binds TCP on Windows")
	}
}

func TestNewReloadStop(t *testing.T) {
	skipIfWindows(t)
	eng, err := New(corefileOn("45353"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop() })

	// A valid reload to a different port must succeed and swap the instance.
	prev := eng.instance
	if err := eng.Reload(corefileOn("45354")); err != nil {
		t.Fatalf("Reload (valid): %v", err)
	}
	if eng.instance == prev {
		t.Fatal("Reload (valid): instance was not swapped")
	}

	// An invalid reload must return an error and leave the previous instance
	// serving (its reference unchanged) — the caller surfaces the error, we do
	// not drop the running config (docs/architecture.md).
	kept := eng.instance
	if err := eng.Reload([]byte("this is not a valid corefile {")); err == nil {
		t.Fatal("Reload (invalid): expected error, got nil")
	}
	if eng.instance != kept {
		t.Fatal("Reload (invalid): instance must be unchanged after a failed reload")
	}

	if err := eng.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// The ecs_enabled setting renders a "rewrite edns0 subnet set 32 128" line
// (configgen); this confirms CoreDNS's own rewrite plugin actually accepts
// that directive syntax, not just that configgen's string-building is right.
func TestNewAcceptsECSRewriteDirective(t *testing.T) {
	skipIfWindows(t)
	cf := []byte(".:45355 {\n\tbind 127.0.0.1\n\trewrite edns0 subnet set 32 128\n\twhoami\n}\n")
	eng, err := New(cf)
	if err != nil {
		t.Fatalf("New with rewrite edns0 subnet directive: %v", err)
	}
	_ = eng.Stop()
}

func TestNewInvalidFails(t *testing.T) {
	skipIfWindows(t)
	eng, err := New([]byte("definitely not a corefile {"))
	if err == nil {
		_ = eng.Stop()
		t.Fatal("New: expected error for invalid Corefile, got nil")
	}
}
