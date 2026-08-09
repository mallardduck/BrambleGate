package mdnsbridge

import (
	"testing"
	"time"

	"github.com/coredns/caddy"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestSetupRegistersLoaded(t *testing.T) {
	SetTable(NewTable(baseCfg(), time.Minute))
	t.Cleanup(func() { SetTable(nil) })

	if err := setup(caddy.NewTestController("dns", "mdnsbridge")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d, s, ok := pluginreg.Get("mdnsbridge")
	if !ok {
		t.Fatal("expected mdnsbridge to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin || d.Required {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if len(d.DependsOn) != 1 || d.DependsOn[0] != "localrecords" {
		t.Fatalf("expected DependsOn=[localrecords], got %v", d.DependsOn)
	}
	if !s.Loaded {
		t.Fatalf("expected mdnsbridge to report Loaded after successful setup, reason=%q", s.Reason)
	}
}

func TestSetupFailureReportsReason(t *testing.T) {
	SetTable(nil)
	t.Cleanup(func() { SetTable(nil) })

	if err := setup(caddy.NewTestController("dns", "mdnsbridge")); err == nil {
		t.Fatal("expected setup to fail without an injected table")
	}

	_, s, ok := pluginreg.Get("mdnsbridge")
	if !ok {
		t.Fatal("expected mdnsbridge to be registered even on setup failure")
	}
	if s.Loaded {
		t.Fatal("expected mdnsbridge to report not-Loaded after setup failure")
	}
	if s.Reason == "" {
		t.Fatal("expected a non-empty failure reason")
	}
}
