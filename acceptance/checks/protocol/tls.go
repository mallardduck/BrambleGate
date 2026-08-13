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
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// TLSChainValidity verifies the DoT/DoH cert chain validates with no
// client-side trust-store changes (roadmap.md Scenario 1, steps 1-3 /
// docs/encrypted-dns.md) — a PKIX/RFC 7858/RFC 8484 conformance check, not a
// BrambleGate-specific one: any correctly-configured DoT/DoH server should
// pass it. A successful tls.Dial without InsecureSkipVerify already implies
// a valid chain against the system trust store, matching what the
// openssl s_client "Verify return code: 0 (ok)" checks in
// testing-guide.md confirmed by hand. The Android/browser client legs stay
// manual.
type TLSChainValidity struct{}

func (c TLSChainValidity) Name() string        { return "protocol/tls-chain-validity" }
func (c TLSChainValidity) Tier() checks.Tier   { return checks.TierNetwork }
func (c TLSChainValidity) Scope() checks.Scope { return checks.ScopeProtocol }

var tlsListeners = []struct {
	port int
	name string
}{
	{853, "DoT"},
	{443, "DoH"},
}

func (c TLSChainValidity) Run(_ context.Context, cfg *config.Config) checks.Result {
	if cfg.Target.DNSAddr == "" || cfg.Domain == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "target.dns_addr or domain not set"}
	}

	var details []string
	for _, l := range tlsListeners {
		addr := net.JoinHostPort(cfg.Target.DNSAddr, fmt.Sprint(l.port))
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Domain})
		if err != nil {
			return checks.Result{
				Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
				Detail: fmt.Sprintf("%s (%s): %v", l.name, addr, err),
			}
		}
		state := conn.ConnectionState()
		conn.Close()
		if len(state.PeerCertificates) == 0 {
			return checks.Result{
				Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
				Detail: fmt.Sprintf("%s (%s): handshake succeeded but no peer certificates", l.name, addr),
			}
		}
		leaf := state.PeerCertificates[0]
		details = append(details, fmt.Sprintf("%s: subject=%s issuer=%s", l.name, leaf.Subject.CommonName, leaf.Issuer.CommonName))
	}

	return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: strings.Join(details, "; ")}
}
