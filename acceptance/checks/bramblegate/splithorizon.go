// Package bramblegate holds config-aware checks: assertions that only make
// sense once you know what this specific BrambleGate instance was configured
// to do (a VLAN's split-horizon override, a hosts.yaml entry, /api/* state).
// See checks/protocol for the DNS-standards-conformance counterpart, and
// checks.Scope for the distinction.
package bramblegate

import (
	"context"
	"fmt"
	"strings"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/dnsutil"
)

// SplitHorizon checks one VLAN's override for config.Domain resolves as
// expected (roadmap.md Scenario 2 / docs/local-records.md). Local-tier: only
// meaningful when this binary runs from a device on cfg.Name's VLAN — the
// answer BrambleGate gives depends on the querying source IP, which this
// check doesn't (and can't) control; running it from the right VLAN is on
// the operator.
type SplitHorizon struct {
	VLAN config.VLAN
}

func (c SplitHorizon) Name() string        { return "splithorizon/" + c.VLAN.Name }
func (c SplitHorizon) Tier() checks.Tier   { return checks.TierLocal }
func (c SplitHorizon) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c SplitHorizon) Run(_ context.Context, cfg *config.Config) checks.Result {
	if cfg.Target.DNSAddr == "" || cfg.Domain == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "target.dns_addr or domain not set"}
	}
	if c.VLAN.ExpectValue == "" && !c.VLAN.ExpectNXDOMAIN {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "vlan has no expect_value/expect_nxdomain configured"}
	}

	resp, err := dnsutil.Query(cfg.Target.DNSAddr, cfg.Domain, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}

	if c.VLAN.ExpectNXDOMAIN {
		if resp.Rcode == dns.RcodeNameError {
			return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: "NXDOMAIN as expected"}
		}
		return checks.Result{
			Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
			Detail: fmt.Sprintf("expected NXDOMAIN, got RCODE=%s answers=%s", dns.RcodeToString[resp.Rcode], strings.Join(dnsutil.AAnswers(resp), ",")),
		}
	}

	got := dnsutil.AAnswers(resp)
	if dnsutil.Contains(got, c.VLAN.ExpectValue) {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: "got " + c.VLAN.ExpectValue}
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
		Detail: fmt.Sprintf("expected %s, got %s", c.VLAN.ExpectValue, strings.Join(got, ",")),
	}
}
