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
	for _, h := range cfg.Hosts {
		all = append(all, bramblegate.Hosts{Entry: h})
	}
	all = append(all, bramblegate.ClientNames{})
	all = append(all, bramblegate.MDNSPromoted{})

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
