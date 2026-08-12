package vlancache

import (
	"testing"
	"time"

	"github.com/coredns/caddy"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestParseBareDirectiveUsesDefaults(t *testing.T) {
	vc, err := parse(caddy.NewTestController("dns", "vlancache"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if vc.failTTL != defaultFailTTL {
		t.Fatalf("want default failTTL %s, got %s", defaultFailTTL, vc.failTTL)
	}
}

func TestParseCapacityAndServfail(t *testing.T) {
	corefile := "vlancache {\n\tcapacity 500\n\tservfail 10s\n}"
	vc, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if vc.failTTL != 10*time.Second {
		t.Fatalf("want failTTL 10s, got %s", vc.failTTL)
	}
}

func TestParseRejectsServfailOverRFC2308Cap(t *testing.T) {
	corefile := "vlancache {\n\tservfail 10m\n}"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error for servfail ttl over the RFC 2308 5m cap")
	}
}

func TestParsePrefetchAndServeStale(t *testing.T) {
	corefile := "vlancache {\n\tprefetch\n\tserve_stale 1h\n}"
	vc, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !vc.prefetch {
		t.Fatal("want prefetch enabled")
	}
	if vc.staleTTL != time.Hour {
		t.Fatalf("want staleTTL 1h, got %s", vc.staleTTL)
	}
}

func TestParseBareDirectiveDisablesPrefetchAndServeStale(t *testing.T) {
	vc, err := parse(caddy.NewTestController("dns", "vlancache"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if vc.prefetch {
		t.Fatal("want prefetch disabled by default")
	}
	if vc.staleTTL != 0 {
		t.Fatalf("want staleTTL 0 (disabled) by default, got %s", vc.staleTTL)
	}
}

func TestParseRejectsPrefetchWithArgument(t *testing.T) {
	corefile := "vlancache {\n\tprefetch 10\n}"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error: prefetch is a bare toggle, not a tunable")
	}
}

func TestParseRejectsNonPositiveServeStale(t *testing.T) {
	corefile := "vlancache {\n\tserve_stale 0s\n}"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error for a non-positive serve_stale duration")
	}
}

func TestParseRejectsInvalidCapacity(t *testing.T) {
	corefile := "vlancache {\n\tcapacity 0\n}"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error for a non-positive capacity")
	}
}

func TestParseRejectsUnknownProperty(t *testing.T) {
	corefile := "vlancache {\n\tbogus yes\n}"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error for an unknown property")
	}
}

func TestParseRejectsMultipleStanzas(t *testing.T) {
	corefile := "vlancache\nvlancache"
	if _, err := parse(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected an error for multiple vlancache stanzas")
	}
}

func TestSetupRegistersLoaded(t *testing.T) {
	if err := setup(caddy.NewTestController("dns", "vlancache")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d, s, ok := pluginreg.Get("vlancache")
	if !ok {
		t.Fatal("expected vlancache to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if !s.Loaded {
		t.Fatalf("expected vlancache to report Loaded after successful setup, reason=%q", s.Reason)
	}
}

func TestSetupFailureReportsReason(t *testing.T) {
	corefile := "vlancache {\n\tbogus yes\n}"
	if err := setup(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected setup to fail on an unknown property")
	}

	_, s, ok := pluginreg.Get("vlancache")
	if !ok {
		t.Fatal("expected vlancache to be registered even on setup failure")
	}
	if s.Loaded {
		t.Fatal("expected vlancache to report not-Loaded after setup failure")
	}
}
