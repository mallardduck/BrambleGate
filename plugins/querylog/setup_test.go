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

func TestSetup_AcrossReloads_SameCapacity_PreservesRingAndHistory(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	// Each caddy.NewTestController call gets its own fresh Instance, same as
	// a genuine CoreDNS reload — the per-parse marker (ringMarker) is absent
	// on both calls. An operator applying a settings change (or any reload)
	// shouldn't lose their query history just because CoreDNS's Instance was
	// torn down and rebuilt underneath BrambleGate, which itself never
	// restarted — so with capacity unchanged, the ring (and its entries)
	// must survive.
	if err := setup(caddy.NewTestController("dns", "querylog")); err != nil {
		t.Fatalf("setup (reload 1): %v", err)
	}
	first := Current()
	if first == nil {
		t.Fatal("expected Current() to be set after reload 1")
	}
	first.Push(Entry{QName: "a"})

	if err := setup(caddy.NewTestController("dns", "querylog")); err != nil {
		t.Fatalf("setup (reload 2): %v", err)
	}
	second := Current()

	if first != second {
		t.Fatal("expected an unchanged-capacity reload to reuse the existing Ring, got a distinct instance")
	}
	if got := len(second.Snapshot(Filter{})); got != 1 {
		t.Fatalf("Snapshot len = %d, want 1 (history lost across reload)", got)
	}
}

func TestSetup_CapacityChange_AllocatesNewRing(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	if err := setup(caddy.NewTestController("dns", "querylog {\n\tcapacity 2\n}")); err != nil {
		t.Fatalf("setup (capacity 2): %v", err)
	}
	first := Current()

	if err := setup(caddy.NewTestController("dns", "querylog {\n\tcapacity 4\n}")); err != nil {
		t.Fatalf("setup (capacity 4): %v", err)
	}
	second := Current()

	if first == second {
		t.Fatal("expected a genuine capacity change (e.g. across a reload) to allocate a fresh Ring")
	}
	if second.Cap() != 4 {
		t.Fatalf("Current().Cap() = %d, want 4", second.Cap())
	}
}

func TestSetup_MarkerAlreadySet_ReusesRingWithoutCapacityCheck(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	// Simulates a later server block within the SAME reload/Instance: a
	// prior querylog block already made the ring decision (ringMarker set),
	// so this block must attach to the existing Ring unconditionally — even
	// though its own capacity differs — rather than re-deciding per block
	// (which is exactly the multi-ring bug this design fixes: a
	// multi-listener Corefile renders one querylog occurrence per listener,
	// all within one reload).
	existing := NewRing(2)
	SetCurrent(existing)

	c := caddy.NewTestController("dns", "querylog {\n\tcapacity 999\n}")
	c.Set(ringMarker{}, true)

	if err := setup(c); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if Current() != existing {
		t.Fatal("expected setup to reuse the already-decided Ring once the per-parse marker is set, regardless of this block's own capacity")
	}
}

func TestSetup_UnknownProperty_ReportsFailure(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })

	corefile := "querylog {\n\tbogus\n}"
	if err := setup(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected setup to fail on an unknown property")
	}
}
