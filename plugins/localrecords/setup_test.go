package localrecords

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coredns/caddy"
	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestSetupRegistersLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.json")
	if err := os.WriteFile(path, []byte(zoneJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	corefile := "localrecords home.arpa {\n\tzonedata " + filepath.ToSlash(path) + "\n}"

	if err := setup(caddy.NewTestController("dns", corefile)); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d, s, ok := pluginreg.Get("localrecords")
	if !ok {
		t.Fatal("expected localrecords to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin || !d.Required {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if !s.Loaded {
		t.Fatalf("expected localrecords to report Loaded after successful setup, reason=%q", s.Reason)
	}
}

func TestSetupFailureReportsReason(t *testing.T) {
	// Missing "zonedata" makes parse() fail.
	corefile := "localrecords home.arpa {\n}"

	if err := setup(caddy.NewTestController("dns", corefile)); err == nil {
		t.Fatal("expected setup to fail on missing zonedata")
	}

	_, s, ok := pluginreg.Get("localrecords")
	if !ok {
		t.Fatal("expected localrecords to be registered even on setup failure")
	}
	if s.Loaded {
		t.Fatal("expected localrecords to report not-Loaded after setup failure")
	}
	if s.Reason == "" {
		t.Fatal("expected a non-empty failure reason")
	}
}
