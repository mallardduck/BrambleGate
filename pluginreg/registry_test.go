package pluginreg_test

import (
	"strings"
	"testing"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestRegisterAndGet(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-a", Kind: pluginreg.CoreDNSPlugin, Required: true})

	d, s, ok := pluginreg.Get("test-a")
	if !ok {
		t.Fatal("expected test-a to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin || !d.Required {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if s.Loaded {
		t.Fatal("expected no state before SetLoaded")
	}
}

func TestGetUnknown(t *testing.T) {
	if _, _, ok := pluginreg.Get("test-does-not-exist"); ok {
		t.Fatal("expected ok=false for unregistered name")
	}
}

func TestSetLoadedAndLoaded(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-b", Kind: pluginreg.BrambleOnly})

	if pluginreg.Loaded("test-b") {
		t.Fatal("expected test-b to start unloaded")
	}

	pluginreg.SetLoaded("test-b", true, "")
	if !pluginreg.Loaded("test-b") {
		t.Fatal("expected test-b to be loaded")
	}

	pluginreg.SetLoaded("test-b", false, "disabled in settings")
	if pluginreg.Loaded("test-b") {
		t.Fatal("expected test-b to be unloaded")
	}
	_, s, _ := pluginreg.Get("test-b")
	if s.Reason != "disabled in settings" {
		t.Fatalf("expected reason to be preserved, got %q", s.Reason)
	}
}

func TestAllIncludesRegistered(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-c", Kind: pluginreg.CoreDNSPlugin})
	pluginreg.SetLoaded("test-c", true, "")

	found := false
	for _, e := range pluginreg.All() {
		if e.Name == "test-c" {
			found = true
			if !e.Loaded {
				t.Fatal("expected test-c entry to report Loaded")
			}
		}
	}
	if !found {
		t.Fatal("expected test-c in All()")
	}
}

func TestValidateMissingDependency(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-dependent", Kind: pluginreg.BrambleOnly, DependsOn: []string{"test-nonexistent-dep"}})

	err := pluginreg.Validate()
	if err == nil {
		t.Fatal("expected error for unregistered dependency")
	}
	if !strings.Contains(err.Error(), "test-nonexistent-dep") {
		t.Fatalf("expected error to name the missing dependency, got: %v", err)
	}
}

func TestValidateRequiredNotLoaded(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-required-unloaded", Kind: pluginreg.CoreDNSPlugin, Required: true})

	err := pluginreg.Validate()
	if err == nil {
		t.Fatal("expected error for unloaded required plugin")
	}
	if !strings.Contains(err.Error(), "test-required-unloaded") {
		t.Fatalf("expected error to name the plugin, got: %v", err)
	}
}

func TestValidateOKWhenRequiredLoadedAndDepsResolved(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-base", Kind: pluginreg.CoreDNSPlugin, Required: true})
	pluginreg.SetLoaded("test-base", true, "")
	pluginreg.Register(pluginreg.Descriptor{Name: "test-dependent-ok", Kind: pluginreg.BrambleOnly, DependsOn: []string{"test-base"}})

	// Validate aggregates errors across every registered descriptor, including
	// ones left over from other tests in this package (global registry) — so
	// assert this specific pair doesn't contribute an error rather than that
	// Validate() returns nil overall.
	err := pluginreg.Validate()
	if err != nil && (strings.Contains(err.Error(), "test-base") || strings.Contains(err.Error(), "test-dependent-ok")) {
		t.Fatalf("did not expect test-base/test-dependent-ok to fail validation: %v", err)
	}
}
