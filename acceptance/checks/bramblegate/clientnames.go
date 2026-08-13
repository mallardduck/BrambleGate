package bramblegate

import (
	"context"
	"fmt"

	"github.com/mallardduck/BrambleGate/acceptance/apiclient"
	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// clientsListResponse mirrors GET /api/clients's JSON shape
// (internal/gui/server.go's listClients / plugins/clientnames.Entry) — only
// the fields this check needs.
type clientsListResponse struct {
	Clients []struct {
		IP       string `json:"ip"`
		Hostname string `json:"hostname"`
		Source   string `json:"source"`
	} `json:"clients"`
}

// ClientNames checks GET /api/clients is reachable and reports what it's
// resolved by source tier. It does not yet assert per-client expectations
// (which real device shows up via which tier) — Config has no
// per-client-IP expectation model, so that half of roadmap.md Phase 9's
// real-network leg stays a manual cross-check against the query log/GUI for
// now; a future config.ClientExpectation list would let this go further.
type ClientNames struct{}

func (c ClientNames) Name() string        { return "clientnames" }
func (c ClientNames) Tier() checks.Tier   { return checks.TierNetwork }
func (c ClientNames) Scope() checks.Scope { return checks.ScopeBrambleGate }

func (c ClientNames) Run(ctx context.Context, cfg *config.Config) checks.Result {
	var list clientsListResponse
	if err := apiclient.GetJSON(ctx, cfg.Target.APIBase, "/api/clients", &list); err != nil {
		return checks.Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Fail, Detail: err.Error()}
	}

	counts := map[string]int{}
	for _, e := range list.Clients {
		src := e.Source
		if src == "" {
			src = "unresolved"
		}
		counts[src]++
	}
	return checks.Result{
		Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: checks.Pass,
		Detail: fmt.Sprintf("%d known clients: hosts=%d mdns=%d ptr=%d unresolved=%d",
			len(list.Clients), counts["hosts"], counts["mdns"], counts["ptr"], counts["unresolved"]),
	}
}
