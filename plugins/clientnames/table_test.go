package clientnames

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
)

type fakeResolver struct {
	mu        sync.Mutex
	calls     int
	hostnames map[string]string
	err       error
}

func (f *fakeResolver) Lookup(_ context.Context, ip string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.hostnames[ip], nil
}

func (f *fakeResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestResolve_HostsTierWins(t *testing.T) {
	tbl := NewTable(Config{HostsIndex: map[string]string{"192.168.1.5": "nas.home.arpa"}})
	name, source := tbl.Resolve("192.168.1.5")
	if name != "nas.home.arpa" || source != string(SourceHosts) {
		t.Fatalf("got %q/%q, want nas.home.arpa/hosts", name, source)
	}
}

func TestResolve_MDNSTierLiveMatch(t *testing.T) {
	mdns := mdnsbridge.NewTable(mdnsbridge.Config{DefaultSuffix: "home.arpa"}, 0)
	mdns.Upsert(mdnsbridge.Entry{
		Host: "printer.local.", Service: "_http._tcp", Instance: "Printer",
		IPv4: []string{"192.168.1.7"},
	})
	tbl := NewTable(Config{MDNS: mdns})

	name, source := tbl.Resolve("192.168.1.7")
	if name != "printer.local" || source != string(SourceMDNS) {
		t.Fatalf("got %q/%q, want printer.local/mdns", name, source)
	}
}

func TestResolve_HostsBeatsMDNS(t *testing.T) {
	mdns := mdnsbridge.NewTable(mdnsbridge.Config{DefaultSuffix: "home.arpa"}, 0)
	mdns.Upsert(mdnsbridge.Entry{Host: "mdns-name.local.", Service: "_http._tcp", Instance: "x", IPv4: []string{"192.168.1.9"}})
	tbl := NewTable(Config{
		HostsIndex: map[string]string{"192.168.1.9": "static.home.arpa"},
		MDNS:       mdns,
	})
	name, source := tbl.Resolve("192.168.1.9")
	if name != "static.home.arpa" || source != string(SourceHosts) {
		t.Fatalf("got %q/%q, want static.home.arpa/hosts (tier 0 must win over tier 1)", name, source)
	}
}

func TestResolve_NoneWhenUnresolved(t *testing.T) {
	tbl := NewTable(Config{})
	name, source := tbl.Resolve("10.0.0.99")
	if name != "" || source != "" {
		t.Fatalf("got %q/%q, want empty/empty for an unresolved IP", name, source)
	}
}

func TestObserve_SkipsPTRWhenTier0Resolves(t *testing.T) {
	resolver := &fakeResolver{hostnames: map[string]string{}}
	tbl := NewTable(Config{
		HostsIndex:        map[string]string{"192.168.1.5": "nas.home.arpa"},
		UnmatchedResolver: resolver,
	})
	tbl.Observe("192.168.1.5", "")
	// No async work should have been queued at all — give the (nonexistent)
	// worker a moment to prove nothing arrives.
	time.Sleep(20 * time.Millisecond)
	if resolver.callCount() != 0 {
		t.Fatalf("PTR resolver called %d times, want 0 (tier 0 already resolved)", resolver.callCount())
	}
}

func TestObserve_QueuesAndResolvesPTR(t *testing.T) {
	resolver := &fakeResolver{hostnames: map[string]string{"10.0.0.5": "laptop"}}
	tbl := NewTable(Config{UnmatchedResolver: resolver, RefreshHostnames: "ipv4_only"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tbl.Run(ctx)

	tbl.Observe("10.0.0.5", "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		name, source := tbl.Resolve("10.0.0.5")
		if source == string(SourcePTR) && name == "laptop" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("PTR tier never resolved 10.0.0.5")
}

func TestObserve_NoResolverForClientsVLANSkipsPTR(t *testing.T) {
	// A resolver exists for "trusted" but this client is on "guest" — no
	// UnmatchedResolver fallback, so PTR must stay off for it.
	trusted := &fakeResolver{hostnames: map[string]string{"192.168.20.5": "should-not-be-queried"}}
	tbl := NewTable(Config{Resolvers: map[string]Resolver{"trusted": trusted}})

	tbl.Observe("192.168.20.5", "guest")
	time.Sleep(20 * time.Millisecond)
	if trusted.callCount() != 0 {
		t.Fatalf("trusted VLAN's resolver called %d times, want 0 (client is on guest, no resolver configured for it)", trusted.callCount())
	}
}

func TestObserve_PerVLANResolverSelection(t *testing.T) {
	trusted := &fakeResolver{hostnames: map[string]string{"192.168.10.5": "trusted-client"}}
	guest := &fakeResolver{hostnames: map[string]string{"192.168.20.5": "guest-client"}}
	tbl := NewTable(Config{Resolvers: map[string]Resolver{"trusted": trusted, "guest": guest}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tbl.Run(ctx)

	tbl.Observe("192.168.10.5", "trusted")
	tbl.Observe("192.168.20.5", "guest")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n1, s1 := tbl.Resolve("192.168.10.5")
		n2, s2 := tbl.Resolve("192.168.20.5")
		if s1 == string(SourcePTR) && n1 == "trusted-client" && s2 == string(SourcePTR) && n2 == "guest-client" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected each client resolved against its own VLAN's resolver")
}

func TestObserve_UnmatchedResolverUsedWhenNoVLAN(t *testing.T) {
	unmatched := &fakeResolver{hostnames: map[string]string{"192.168.99.5": "flat-network-client"}}
	tbl := NewTable(Config{UnmatchedResolver: unmatched})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tbl.Run(ctx)

	tbl.Observe("192.168.99.5", "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		name, source := tbl.Resolve("192.168.99.5")
		if source == string(SourcePTR) && name == "flat-network-client" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the unmatched-VLAN client to resolve via UnmatchedResolver")
}

func TestSweep_RefreshModes(t *testing.T) {
	resolver := &fakeResolver{hostnames: map[string]string{
		"10.0.0.1": "v4-client",
		"::1":      "v6-client",
	}}
	tbl := NewTable(Config{UnmatchedResolver: resolver})
	now := time.Now()
	tbl.now = func() time.Time { return now }

	// Seed both entries as already PTR-resolved, no VLAN (uses UnmatchedResolver).
	tbl.entries["10.0.0.1"] = &Entry{IP: "10.0.0.1", Hostname: "v4-client", Source: SourcePTR}
	tbl.entries["::1"] = &Entry{IP: "::1", Hostname: "v6-client", Source: SourcePTR}

	tbl.cfg.RefreshHostnames = "none"
	if n := tbl.Sweep(); n != 0 {
		t.Fatalf("none mode swept %d entries, want 0", n)
	}

	tbl.cfg.RefreshHostnames = "ipv4_only"
	if n := tbl.Sweep(); n != 1 {
		t.Fatalf("ipv4_only mode swept %d entries, want 1 (skip the IPv6 entry)", n)
	}

	tbl.cfg.RefreshHostnames = "all"
	if n := tbl.Sweep(); n != 2 {
		t.Fatalf("all mode swept %d entries, want 2", n)
	}
}

func TestSnapshotSorted(t *testing.T) {
	tbl := NewTable(Config{})
	tbl.Observe("10.0.0.9", "")
	tbl.Observe("10.0.0.2", "")
	tbl.Observe("10.0.0.5", "")

	got := tbl.Snapshot()
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	want := []string{"10.0.0.2", "10.0.0.5", "10.0.0.9"}
	for i, ip := range want {
		if got[i].IP != ip {
			t.Fatalf("entry[%d].IP = %q, want %q", i, got[i].IP, ip)
		}
	}
}

func TestSetConfigTakesEffectLive(t *testing.T) {
	tbl := NewTable(Config{})
	if name, _ := tbl.Resolve("192.168.1.5"); name != "" {
		t.Fatalf("expected no resolution before SetConfig, got %q", name)
	}
	tbl.SetConfig(Config{HostsIndex: map[string]string{"192.168.1.5": "nas.home.arpa"}})
	if name, source := tbl.Resolve("192.168.1.5"); name != "nas.home.arpa" || source != string(SourceHosts) {
		t.Fatalf("got %q/%q after SetConfig, want nas.home.arpa/hosts", name, source)
	}
}
