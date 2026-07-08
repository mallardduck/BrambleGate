package mdnsbridge

import "net"

// NamingRule maps discoveries matching Match to a DNS suffix (overriding the
// default). First matching rule wins.
type NamingRule struct {
	Match  Selector
	Suffix string
}

// Config drives how the Table maps, publishes, and resolves discoveries. It is
// built by the host process (cli) from settings.yaml + records.yaml and applied
// to the Table; it can be replaced live on a config change.
type Config struct {
	// DefaultSuffix is the zone names map into (e.g. "home.arpa").
	DefaultSuffix string
	// AutoPublish serves a discovery live when any selector matches.
	AutoPublish SelectorSet
	// Naming overrides the suffix for matching discoveries.
	Naming []NamingRule
	// Promoted binds a fully-qualified DNS name to the selector that resolves it
	// (from a type:mdns record). Keyed by normalized fqdn.
	Promoted map[string]Selector
	// VLANs maps VLAN name to CIDRs, for the selector VLAN condition.
	VLANs map[string][]*net.IPNet
}

// suffixFor returns the naming suffix for a discovered entry.
func (c Config) suffixFor(e Entry) string {
	for _, r := range c.Naming {
		if r.Match.Match(e, c.VLANs) {
			return r.Suffix
		}
	}
	return c.DefaultSuffix
}
