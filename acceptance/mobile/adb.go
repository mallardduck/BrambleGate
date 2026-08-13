// Package mobile is the deferred ADB-backed tier: checks that need a
// connected Android device to prove real Private DNS behavior
// (roadmap.md Scenario 1 step 4/5) instead of just a TLS-layer proxy for it.
// Off by default (config.Mobile.Enabled); not built yet — see checks.TierMobile.
//
// Candidate libraries for the real client, instead of shelling out to the adb
// binary: github.com/prife/goadb or github.com/taigrr/adb. Neither is a
// dependency yet — pick when this tier is actually implemented.
package mobile

import (
	"context"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// Available reports whether the mobile tier can run at all right now —
// config.Mobile.Enabled and a reachable device. Always false until a real
// ADB client is wired in.
func Available(cfg *config.Config) bool {
	return false
}

// PrivateDNSMode checks the device's actual Private DNS setting/behavior
// (roadmap.md Scenario 1 step 4: strict/manual-hostname mode, and the
// Automatic/opportunistic-mode leg already validated by hand in
// testing-guide.md). Stub — see package doc for candidate libraries.
type PrivateDNSMode struct{}

func (c PrivateDNSMode) Name() string        { return "mobile/private-dns-mode" }
func (c PrivateDNSMode) Tier() checks.Tier   { return checks.TierMobile }
func (c PrivateDNSMode) Scope() checks.Scope { return checks.ScopeProtocol }

func (c PrivateDNSMode) Run(_ context.Context, cfg *config.Config) checks.Result {
	if !cfg.Mobile.Enabled {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "mobile tier disabled (mobile.enabled: false)"}
	}
	return checks.Todo(c, "not implemented: needs goadb or taigrr/adb client, see package doc")
}
