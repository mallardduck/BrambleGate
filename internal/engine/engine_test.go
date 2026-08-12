package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/miekg/dns"
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

// The forward-tuning settings render a "forward . <addr> { ... }" sub-block
// (configgen.writeForward); this confirms CoreDNS's own forward plugin
// actually accepts that block syntax, not just that configgen's
// string-building is right.
func TestNewAcceptsForwardTuningDirectives(t *testing.T) {
	skipIfWindows(t)
	cf := []byte(".:45356 {\n\tbind 127.0.0.1\n\tforward . 127.0.0.1:53 {\n\t\tmax_fails 0\n\t\thealth_check 30s\n\t\texpire 20s\n\t\tprefer_udp\n\t\tmax_concurrent 500\n\t}\n}\n")
	eng, err := New(cf)
	if err != nil {
		t.Fatalf("New with forward tuning sub-block: %v", err)
	}
	_ = eng.Stop()
}

// The hosts directive (dev-docs/static-hosts.md, Phase 8 migration step 1)
// must both (a) actually override the name it lists and (b) fall through to
// whatever's next in the chain for anything it doesn't — confirmed against a
// real hosts-format file and real DNS queries over the wire, not just that
// New() accepts the Corefile syntax. whoami is the fallthrough target: it
// answers any A/AAAA query with the requester's own address, which is
// trivially distinguishable from the hosts entry's static answer.
func TestNewHostsOverrideWinsAndFallsThrough(t *testing.T) {
	skipIfWindows(t)
	hostsPath := filepath.Join(t.TempDir(), "hosts.txt")
	if err := os.WriteFile(hostsPath, []byte("192.0.2.55 override.example.\n"), 0o644); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
	cf := []byte(".:45358 {\n\tbind 127.0.0.1\n\thosts " + filepath.ToSlash(hostsPath) + " . {\n\t\tfallthrough\n\t}\n\twhoami\n}\n")
	eng, err := New(cf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Stop() })

	c := new(dns.Client)

	override := new(dns.Msg)
	override.SetQuestion("override.example.", dns.TypeA)
	resp, _, err := c.Exchange(override, "127.0.0.1:45358")
	if err != nil {
		t.Fatalf("query override.example: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("want 1 answer from the hosts override, got %d: %+v", len(resp.Answer), resp.Answer)
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() != "192.0.2.55" {
		t.Fatalf("want the hosts override answer 192.0.2.55, got %+v", resp.Answer[0])
	}

	miss := new(dns.Msg)
	miss.SetQuestion("not-in-hosts.example.", dns.TypeA)
	resp2, _, err := c.Exchange(miss, "127.0.0.1:45358")
	if err != nil {
		t.Fatalf("query not-in-hosts.example: %v", err)
	}
	// whoami answers via the Additional section (its own A/SRV records go in
	// Extra, never Answer — see plugin/whoami's ServeDNS), so a fallthrough
	// here is confirmed by Extra, unlike the hosts override above.
	if len(resp2.Answer) != 0 {
		t.Fatalf("want 0 Answer entries from whoami's fallthrough, got %d: %+v", len(resp2.Answer), resp2.Answer)
	}
	if len(resp2.Extra) != 2 {
		t.Fatalf("want 2 fallthrough Extra records from whoami, got %d: %+v", len(resp2.Extra), resp2.Extra)
	}
	if a, ok := resp2.Extra[0].(*dns.A); !ok || a.A.String() == "192.0.2.55" {
		t.Fatalf("want a fallthrough answer distinct from the hosts override, got %+v", resp2.Extra[0])
	}
}

func TestNewInvalidFails(t *testing.T) {
	skipIfWindows(t)
	eng, err := New([]byte("definitely not a corefile {"))
	if err == nil {
		_ = eng.Stop()
		t.Fatal("New: expected error for invalid Corefile, got nil")
	}
}
