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
	// Resolver is the tier-2 lookup (PTR). nil disables the PTR tier — Table
	// then runs hosts+mDNS-only, per client_names.ptr_upstream being unset.
	Resolver Resolver
	// RefreshHostnames governs the tier-2 hourly re-sweep of already-cached
	// PTR entries: "ipv4_only" (default), "all", or "none". Mirrors Pi-hole's
	// REFRESH_HOSTNAMES.
	RefreshHostnames string
}
