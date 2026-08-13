package bramblegate

import (
	"context"
	"fmt"
	"strings"

	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/acceptance/apiclient"
	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
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

// MDNSPromoted checks a previously-promoted mDNS record still resolves and
// tracks the device's live state via GET /api/mdns (roadmap.md Scenario 3,
// steps 1-2 only — power-cycle and self-advertisement browse stay manual).
type MDNSPromoted struct{}

func (c MDNSPromoted) Name() string        { return "mdns/promoted" }
func (c MDNSPromoted) Tier() checks.Tier   { return checks.TierNetwork }
func (c MDNSPromoted) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c MDNSPromoted) Run(ctx context.Context, cfg *config.Config) checks.Result {
	if cfg.MDNS.PromotedName == "" {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Skip, Detail: "mdns.promoted_name not set"}
	}

	resp, err := dnsutil.Query(cfg.Target.DNSAddr, cfg.MDNS.PromotedName, dns.TypeA)
	if err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}
	dnsIPs := dnsutil.AAnswers(resp)
	if len(dnsIPs) == 0 {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: cfg.MDNS.PromotedName + " did not resolve"}
	}

	var list mdnsListResponse
	if err := apiclient.GetJSON(ctx, cfg.Target.APIBase, "/api/mdns", &list); err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}

	want := strings.TrimSuffix(dns.Fqdn(cfg.MDNS.PromotedName), ".")
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
		Detail: cfg.MDNS.PromotedName + " not found in /api/mdns entries",
	}
}
