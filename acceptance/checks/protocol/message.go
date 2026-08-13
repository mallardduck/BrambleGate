package protocol

import (
	"context"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// AuthoritativeAnswerConformance checks that queries for a name BrambleGate
// is authoritative for (cfg.Domain's zone) get NXDOMAIN with the AA bit set
// for a nonexistent subdomain, not REFUSED or a silent drop — RFC 1035
// authoritative-answer semantics, not a BrambleGate-specific behavior.
type AuthoritativeAnswerConformance struct{}

func (c AuthoritativeAnswerConformance) Name() string        { return "protocol/authoritative-nxdomain" }
func (c AuthoritativeAnswerConformance) Tier() checks.Tier   { return checks.TierNetwork }
func (c AuthoritativeAnswerConformance) Scope() checks.Scope { return checks.ScopeProtocol }

func (c AuthoritativeAnswerConformance) Run(_ context.Context, cfg *config.Config) checks.Result {
	// TODO: query "acceptance-nonexistent-check." + cfg.Domain against
	// cfg.Target.DNSAddr, assert RCODE=NXDOMAIN + AA bit set (not REFUSED,
	// not a timeout) — miekg/dns, same library already vendored root-side.
	return checks.Todo(c, "not implemented: NXDOMAIN/AA-bit conformance query")
}

// TCPFallback checks a truncated (TC-bit) UDP response is correctly
// retryable over TCP — RFC 1035 section 4.2.1, independent of BrambleGate's
// specific record content.
type TCPFallback struct{}

func (c TCPFallback) Name() string        { return "protocol/tcp-fallback" }
func (c TCPFallback) Tier() checks.Tier   { return checks.TierNetwork }
func (c TCPFallback) Scope() checks.Scope { return checks.ScopeProtocol }

func (c TCPFallback) Run(_ context.Context, cfg *config.Config) checks.Result {
	// TODO: force a large/truncated response (e.g. a query type likely to
	// trip the UDP size limit) over UDP, confirm TC bit, then confirm the
	// same query succeeds over TCP against cfg.Target.DNSAddr.
	return checks.Todo(c, "not implemented: UDP truncation + TCP retry")
}
