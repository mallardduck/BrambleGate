package clientnames

import "github.com/mallardduck/BrambleGate/plugins/mdnsbridge"

// Config drives how a Table resolves client IPs. Built by the host process
// (internal/gui/service.go, internal/cli) from settings.yaml + hosts.yaml and
// applied via Table.SetConfig; it can be replaced live on a config change.
type Config struct {
	// HostsIndex is the tier-0 lookup: IP -> canonical hostname, built from
	// hosts.yaml's Host.Hostname (never Aliases — dev-docs/client-names.md
	// wants one canonical name per IP). Read live on every resolve, never
	// copied into a Table entry.
	HostsIndex map[string]string
	// MDNS is the tier-1 lookup: mdnsbridge's live discovery table, matched by
	// address. nil when mDNS discovery is disabled.
	MDNS *mdnsbridge.Table
	// Resolvers is the tier-2 (PTR) target per VLAN name — keyed the same as
	// model.VLAN.Name. Either every VLAN's entry points at the one explicit
	// client_names.ptr_upstream override, or (when that's unset) each VLAN's
	// own auto-detected gateway (internal/gatewaydetect), so a client in
	// "trusted" and a client in "guest" can resolve PTR against their own
	// VLAN's router rather than one that likely can't see them
	// (dev-docs/client-names.md). A VLAN with no entry has the PTR tier off.
	Resolvers map[string]Resolver
	// UnmatchedResolver is the tier-2 target for a client whose source IP
	// matched no declared VLAN — only set when client_names.ptr_upstream is
	// an explicit override, or gatewaydetect found a Primary gateway with no
	// VLAN configured at all (e.g. a single flat home network).
	UnmatchedResolver Resolver
	// RefreshHostnames governs the tier-2 hourly re-sweep of already-cached
	// PTR entries: "ipv4_only" (default), "all", or "none". Mirrors Pi-hole's
	// REFRESH_HOSTNAMES.
	RefreshHostnames string
}

// resolverFor picks cfg's PTR target for a client in vlan (""=unmatched) —
// a free function, not a Table method, so it's usable without a lock and
// trivially unit testable on its own.
func resolverFor(cfg Config, vlan string) Resolver {
	if vlan == "" {
		return cfg.UnmatchedResolver
	}
	return cfg.Resolvers[vlan]
}
