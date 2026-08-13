package clientnames

import "time"

// Source identifies which tier resolved an Entry's Hostname (dev-docs/client-names.md).
type Source string

const (
	// SourceHosts is tier 0: a static hosts.yaml entry, matched by IP (read
	// live off the injected index, never copied — see Table.SetConfig).
	SourceHosts Source = "hosts"
	// SourceMDNS is tier 1: a live address match against mdnsbridge.Table,
	// re-checked on every Resolve call rather than cached.
	SourceMDNS Source = "mdns"
	// SourcePTR is tier 2: a cached reverse-DNS lookup result.
	SourcePTR Source = "ptr"
	// SourceNone means the IP has been seen but no tier has resolved a name
	// for it (yet).
	SourceNone Source = ""
)

// Entry is one known client IP and whatever name has been resolved for it.
// Hostname/Source reflect the PTR tier only when that's the best available
// answer — Table.Resolve/Snapshot prefer the live hosts/mDNS tiers over
// whatever's cached here (see Table.resolve).
type Entry struct {
	IP string `json:"ip"`
	// VLAN is "" when the client's address matched no configured VLAN
	// (mirrors querylog.ClientInfo.VLAN) — also which PTR resolver
	// (Config.Resolvers vs UnmatchedResolver) a re-sweep uses for this
	// entry.
	VLAN       string    `json:"vlan,omitempty"`
	Hostname   string    `json:"hostname,omitempty"`
	Source     Source    `json:"source,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}
