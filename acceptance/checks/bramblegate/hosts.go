package bramblegate

import (
	"context"
	"fmt"
	"strings"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/discover"
	"github.com/mallardduck/BrambleGate/acceptance/dnsutil"
)

// Hosts checks one hosts.yaml entry (discovered via GET /api/hosts — see
// discover.Host) resolves to its configured IP for its hostname and every
// alias (roadmap.md Phase 8 real-network leg). Only checks the resolved
// value — proving it "wins over" a colliding records.yaml name requires
// knowing that record's own conflicting value, which discovery doesn't model;
// that half stays a manual cross-check for now.
type Hosts struct {
	Entry discover.Host
}

func (c Hosts) Name() string        { return "hosts/" + c.Entry.Hostname }
func (c Hosts) Tier() checks.Tier   { return checks.TierNetwork }
func (c Hosts) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c Hosts) Run(_ context.Context, cfg *config.Config) checks.Result {
	var mismatches []string
	for _, name := range c.Entry.Names() {
		resp, err := dnsutil.Query(cfg.Target.DNSAddr, name, dns.TypeA)
		if err != nil {
			return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: name + ": " + err.Error()}
		}
		got := dnsutil.AAnswers(resp)
		if !dnsutil.Contains(got, c.Entry.IP) {
			mismatches = append(mismatches, fmt.Sprintf("%s: expected %s, got %s", name, c.Entry.IP, strings.Join(got, ",")))
		}
	}
	if len(mismatches) > 0 {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: strings.Join(mismatches, "; ")}
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass,
		Detail: fmt.Sprintf("%s -> %s", strings.Join(c.Entry.Names(), ","), c.Entry.IP),
	}
}

// HostsDiscovery is a placeholder shown by `list` without --online: it
// stands in for however many Hosts checks discovery would actually produce
// (one per live /api/hosts entry), without needing network access to know
// the count.
type HostsDiscovery struct{}

func (c HostsDiscovery) Name() string        { return "hosts/* (per live /api/hosts entry)" }
func (c HostsDiscovery) Tier() checks.Tier   { return checks.TierNetwork }
func (c HostsDiscovery) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c HostsDiscovery) Run(_ context.Context, _ *config.Config) checks.Result {
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip,
		Detail: "run with a live target, or `list --online`, to discover hosts.yaml entries",
	}
}
