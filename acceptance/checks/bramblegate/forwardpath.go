package bramblegate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/dnsutil"
)

// ForwardPath checks that a query outside BrambleGate's owned zone reaches
// config.Upstream transparently — the same answer set through BrambleGate as
// direct against the upstream (roadmap.md Scenario 4 / docs/forwarding.md).
// The leak-proof half of that scenario (an in-zone name never appearing in
// the upstream's own query log) needs upstream API/log access Config
// doesn't model — stays a manual step.
type ForwardPath struct{}

func (c ForwardPath) Name() string        { return "forwardpath" }
func (c ForwardPath) Tier() checks.Tier   { return checks.TierNetwork }
func (c ForwardPath) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c ForwardPath) Run(_ context.Context, cfg *config.Config) checks.Result {
	if cfg.Upstream.Address == "" || cfg.Upstream.TestDomain == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "upstream.address or upstream.test_domain not set"}
	}

	viaBramble, err := dnsutil.Query(cfg.Target.DNSAddr, cfg.Upstream.TestDomain, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: "via BrambleGate: " + err.Error()}
	}
	viaUpstream, err := dnsutil.Query(cfg.Upstream.Address, cfg.Upstream.TestDomain, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: "direct to upstream: " + err.Error()}
	}

	a, b := dnsutil.AAnswers(viaBramble), dnsutil.AAnswers(viaUpstream)
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, ",") == strings.Join(b, ",") {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass, Detail: fmt.Sprintf("both resolved %s -> %v", cfg.Upstream.TestDomain, a)}
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
		Detail: fmt.Sprintf("via BrambleGate=%v, direct-to-upstream=%v for %s", a, b, cfg.Upstream.TestDomain),
	}
}
