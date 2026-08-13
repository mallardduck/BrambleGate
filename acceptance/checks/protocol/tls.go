// Package protocol holds DNS-standards-conformance checks: assertions true
// of any spec-compliant DoT/DoH/DNS server, independent of what BrambleGate
// specifically has configured. In principle these could point at any DNS
// server, not just BrambleGate — they only need a target address and a
// domain to probe, never VLAN overrides or hosts.yaml content. See
// checks/bramblegate for the BrambleGate-config-aware counterpart, and
// checks.Scope for the distinction.
package protocol

import (
	"context"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// TLSChainValidity verifies the DoT/DoH cert chain validates with no
// client-side trust-store changes (roadmap.md Scenario 1, steps 1-3 /
// docs/encrypted-dns.md) — a PKIX/RFC 7858/RFC 8484 conformance check, not a
// BrambleGate-specific one: any correctly-configured DoT/DoH server should
// pass it. The Android/browser client legs stay manual.
type TLSChainValidity struct{}

func (c TLSChainValidity) Name() string        { return "protocol/tls-chain-validity" }
func (c TLSChainValidity) Tier() checks.Tier   { return checks.TierNetwork }
func (c TLSChainValidity) Scope() checks.Scope { return checks.ScopeProtocol }

func (c TLSChainValidity) Run(_ context.Context, cfg *config.Config) checks.Result {
	// TODO: crypto/tls.Dial cfg.Target.DNSAddr:853 (DoT) and :443 (DoH) with
	// ServerName: cfg.Domain, verify chain + no InsecureSkipVerify, matching
	// the openssl s_client checks already done by hand in testing-guide.md.
	return checks.Todo(c, "not implemented: tls dial + chain verification")
}
