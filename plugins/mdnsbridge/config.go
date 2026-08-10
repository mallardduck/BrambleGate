package mdnsbridge

// NamingRule maps discoveries matching Match to a DNS suffix (overriding the
// default). First matching rule wins.
type NamingRule struct {
	Match  Selector
	Suffix string
}

// Config drives how the Table maps, publishes, and resolves discoveries. It is
// built by the host process (cli) from settings.yaml + records.yaml and applied
// to the Table; it can be replaced live on a config change.
//
// VLANs are deliberately not part of Config: a selector's VLAN condition
// reads vlanmatch.Current() directly (selector.go), the same process-wide
// configured-VLANs source of truth localrecords/querylog use, instead of
// carrying its own independently-refreshed copy (dev-docs/query-log.md).
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
}

// suffixFor returns the naming suffix for a discovered entry.
func (c Config) suffixFor(e Entry) string {
	for _, r := range c.Naming {
		if r.Match.Match(e) {
			return r.Suffix
		}
	}
	return c.DefaultSuffix
}
