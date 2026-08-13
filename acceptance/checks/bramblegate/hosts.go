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

// Hosts checks one hosts.yaml entry resolves and wins over a colliding
// records.yaml name (roadmap.md Phase 8 real-network leg). Only checks the
// resolved value — proving it "wins over" a collision requires knowing
// records.yaml's own conflicting value, which isn't modeled in Config; that
// half stays a manual cross-check for now.
type Hosts struct {
	Entry config.HostsCheck
}

func (c Hosts) Name() string        { return fmt.Sprintf("hosts/%s", c.Entry.Name) }
func (c Hosts) Tier() checks.Tier   { return checks.TierNetwork }
func (c Hosts) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c Hosts) Run(_ context.Context, cfg *config.Config) checks.Result {
	if c.Entry.ExpectIP == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "entry has no expect_ip configured"}
	}
	resp, err := dnsutil.Query(cfg.Target.DNSAddr, c.Entry.Name, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}
	got := dnsutil.AAnswers(resp)
	if dnsutil.Contains(got, c.Entry.ExpectIP) {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: fmt.Sprintf("got %s", c.Entry.ExpectIP)}
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
		Detail: fmt.Sprintf("expected %s, got %s", c.Entry.ExpectIP, strings.Join(got, ",")),
	}
}
