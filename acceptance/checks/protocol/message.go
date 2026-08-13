package protocol

import (
	"context"
	"fmt"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/dnsutil"
)

// AuthoritativeAnswerConformance checks that queries for a name BrambleGate
// is authoritative for (cfg.Domain's zone) get NXDOMAIN with the AA bit set
// for a nonexistent subdomain, not REFUSED or a silent drop — RFC 1035
// authoritative-answer semantics, not a BrambleGate-specific behavior.
type AuthoritativeAnswerConformance struct{}

func (c AuthoritativeAnswerConformance) Name() string      { return "protocol/authoritative-nxdomain" }
func (c AuthoritativeAnswerConformance) Tier() checks.Tier { return checks.TierNetwork }
func (c AuthoritativeAnswerConformance) Scope() checks.Scope {
	return checks.ScopeProtocol
}

func (c AuthoritativeAnswerConformance) Run(_ context.Context, cfg *config.Config) checks.Result {
	if cfg.Target.DNSAddr == "" || cfg.Domain == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "target.dns_addr or domain not set"}
	}

	name := "acceptance-nonexistent-check." + cfg.Domain
	resp, err := dnsutil.Query(cfg.Target.DNSAddr, name, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}
	if resp.Rcode != dns.RcodeNameError {
		return checks.Result{
			Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
			Detail: fmt.Sprintf("expected NXDOMAIN for %s, got RCODE=%s", name, dns.RcodeToString[resp.Rcode]),
		}
	}
	if !resp.Authoritative {
		return checks.Result{
			Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
			Detail: fmt.Sprintf("NXDOMAIN for %s but AA bit not set — not answering authoritatively for its own zone", name),
		}
	}
	return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: "NXDOMAIN+AA for " + name}
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
	// same query succeeds over TCP against cfg.Target.DNSAddr. Needs a
	// record known to be large enough to truncate, which nothing in Config
	// models yet — deferred, unlike the other checks in this file.
	return checks.Todo(c, "not implemented: UDP truncation + TCP retry")
}
