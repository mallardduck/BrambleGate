package main

import (
	"github.com/mallardduck/BrambleGate/acceptance/checks"
	"github.com/mallardduck/BrambleGate/acceptance/checks/bramblegate"
	"github.com/mallardduck/BrambleGate/acceptance/checks/protocol"
	"github.com/mallardduck/BrambleGate/acceptance/config"
	"github.com/mallardduck/BrambleGate/acceptance/mobile"
)

// Registry builds the full check list from cfg — one entry per VLAN/hosts
// override/etc., so the suite's coverage grows with the config file, not with
// hand-maintained call sites.
func Registry(cfg *config.Config) []checks.Check {
	var all []checks.Check

	// checks/protocol: DNS-standards conformance, independent of BrambleGate's
	// specific configured content — only need a target + domain to probe.
	all = append(all,
		protocol.TLSChainValidity{},
		protocol.AuthoritativeAnswerConformance{},
		protocol.TCPFallback{},
	)

	// checks/bramblegate: BrambleGate-config-aware — meaningless without
	// knowing what was configured to happen.
	for _, v := range cfg.VLANs {
		all = append(all, bramblegate.SplitHorizon{VLAN: v})
	}
	all = append(all, bramblegate.ForwardPath{})
	all = append(all, bramblegate.ClientNames{})

	// Hosts and MDNSPromoted scale with live-discovered server state
	// (GET /api/hosts, GET /api/records) rather than acceptance.yaml, so
	// their exact count is only known once cfg.Discovered is populated
	// (always true under `run`; only true under `list --online`). Without
	// it, a single placeholder stands in for the whole category.
	if cfg.Discovered != nil {
		for _, h := range cfg.Discovered.Hosts {
			all = append(all, bramblegate.Hosts{Entry: h})
		}
		for _, r := range cfg.Discovered.MDNSRecords {
			all = append(all, bramblegate.MDNSPromoted{Record: r})
		}
	} else {
		all = append(all, bramblegate.HostsDiscovery{})
		all = append(all, bramblegate.MDNSDiscovery{})
	}

	if cfg.Mobile.Enabled {
		all = append(all, mobile.PrivateDNSMode{})
	}

	return all
}

// FilterTier returns only checks matching tier, or all checks if tier is "".
func FilterTier(all []checks.Check, tier checks.Tier) []checks.Check {
	if tier == "" {
		return all
	}
	var out []checks.Check
	for _, c := range all {
		if c.Tier() == tier {
			out = append(out, c)
		}
	}
	return out
}

// FilterScope returns only checks matching scope, or all checks if scope is "".
func FilterScope(all []checks.Check, scope checks.Scope) []checks.Check {
	if scope == "" {
		return all
	}
	var out []checks.Check
	for _, c := range all {
		if c.Scope() == scope {
			out = append(out, c)
		}
	}
	return out
}
