package querylog

import (
	"testing"

	"github.com/coredns/caddy"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestSetup_Bare_RegistersLoadedWithDefaultCapacity(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	if err := setup(caddy.NewTestController("dns", "querylog")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d, s, ok := pluginreg.Get("querylog")
	if !ok {
		t.Fatal("expected querylog to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if !s.Loaded {
		t.Fatalf("expected querylog to report Loaded after successful setup, reason=%q", s.Reason)
	}

	if Current() == nil {
		t.Fatal("expected Current() to return the ring wired by setup")
	}
	if got := len(Current().Snapshot(Filter{})); got != 0 {
		t.Errorf("fresh ring has %d entries, want 0", got)
	}
}

func TestSetup_WithCapacity(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	corefile := "querylog {\n\tcapacity 2\n}"
	if err := setup(caddy.NewTestController("dns", corefile)); err != nil {
		t.Fatalf("setup: %v", err)
	}

	r := Current()
	r.Push(Entry{QName: "a"})
	r.Push(Entry{QName: "b"})
	r.Push(Entry{QName: "c"}) // evicts "a" if capacity 2 took effect

	got := r.Snapshot(Filter{})
	if len(got) != 2 {
		t.Fatalf("Snapshot len = %d, want 2 (capacity 2 not applied)", len(got))
	}
}

func TestSetup_InvalidCapacity_ReportsFailure(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	corefile := "querylog {\n\tcapacity notanumber\n}"
	if err := setup(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected setup to fail on invalid capacity")
	}

	_, s, ok := pluginreg.Get("querylog")
	if !ok {
		t.Fatal("expected querylog to be registered even on setup failure")
	}
	if s.Loaded {
		t.Fatal("expected querylog to report not-Loaded after setup failure")
	}
	if s.Reason == "" {
		t.Fatal("expected a non-empty failure reason")
	}
}

func TestSetup_UnexpectedArg_ReportsFailure(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	if err := setup(caddy.NewTestController("dns", "querylog extra-arg")); err == nil {
		t.Fatal("expected setup to fail on an unexpected argument")
	}
}

func TestSetup_UnknownProperty_ReportsFailure(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	corefile := "querylog {\n\tbogus\n}"
	if err := setup(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected setup to fail on an unknown property")
	}
}
