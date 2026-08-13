package bramblegate

import (
	"context"
	"fmt"
	"strings"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/apiclient"
	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/discover"
	"github.com/mallardduck/BrambleGate/acceptance/dnsutil"
)

// mdnsListResponse mirrors GET /api/mdns's JSON shape
// (internal/gui/server.go's listMDNS) — only the fields this check needs.
type mdnsListResponse struct {
	Entries []struct {
		Name string   `json:"name"`
		IPv4 []string `json:"ipv4"`
	} `json:"entries"`
}

// MDNSPromoted checks one type:mdns records.yaml entry (discovered via
// GET /api/records — see discover.MDNSRecord) still resolves and tracks the
// device's live state via GET /api/mdns (roadmap.md Scenario 3, steps 1-2
// only — power-cycle and self-advertisement browse stay manual).
type MDNSPromoted struct {
	Record discover.MDNSRecord
}

func (c MDNSPromoted) Name() string        { return "mdns/" + c.Record.Name }
func (c MDNSPromoted) Tier() checks.Tier   { return checks.TierNetwork }
func (c MDNSPromoted) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c MDNSPromoted) Run(ctx context.Context, cfg *config.Config) checks.Result {
	resp, err := dnsutil.Query(cfg.Target.DNSAddr, c.Record.Name, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}
	dnsIPs := dnsutil.AAnswers(resp)
	if len(dnsIPs) == 0 {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: c.Record.Name + " did not resolve"}
	}

	var list mdnsListResponse
	if err := apiclient.GetJSON(ctx, cfg.Target.APIBase, "/api/mdns", &list); err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}

	want := strings.TrimSuffix(dns.Fqdn(c.Record.Name), ".")
	for _, e := range list.Entries {
		if strings.TrimSuffix(dns.Fqdn(e.Name), ".") != want {
			continue
		}
		for _, ip := range dnsIPs {
			if dnsutil.Contains(e.IPv4, ip) {
				return checks.Result{
					Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass,
					Detail: fmt.Sprintf("dig=%s /api/mdns live ipv4=%v, matched %s", dnsIPs, e.IPv4, ip),
				}
			}
		}
		return checks.Result{
			Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
			Detail: fmt.Sprintf("dig=%v does not match /api/mdns live ipv4=%v — resolving a stale/frozen value", dnsIPs, e.IPv4),
		}
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail,
		Detail: c.Record.Name + " not found in /api/mdns entries",
	}
}

// MDNSDiscovery is a placeholder shown by `list` without --online: it stands
// in for however many MDNSPromoted checks discovery would actually produce
// (one per type:mdns records.yaml entry), without needing network access to
// know the count.
type MDNSDiscovery struct{}

func (c MDNSDiscovery) Name() string        { return "mdns/* (per live mdns-linked record)" }
func (c MDNSDiscovery) Tier() checks.Tier   { return checks.TierNetwork }
func (c MDNSDiscovery) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c MDNSDiscovery) Run(_ context.Context, _ *config.Config) checks.Result {
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip,
		Detail: "run with a live target, or `list --online`, to discover mdns-linked records",
	}
}
