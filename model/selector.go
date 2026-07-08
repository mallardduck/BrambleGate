package model

// Selector matches a discovered mDNS-SD entry by its fields. Every set field must
// match (AND); an unset field is a wildcard; values are case-insensitive globs.
// It is the shared primitive for mDNS auto-publish filtering, naming overrides,
// and promoted-record targeting (docs/plugins.md). The mdnsbridge plugin
// re-declares an identical shape (it can't import model); cli translates between
// them.
type Selector struct {
	Service  string            `yaml:"service,omitempty" json:"service,omitempty"`
	Instance string            `yaml:"instance,omitempty" json:"instance,omitempty"`
	Host     string            `yaml:"host,omitempty" json:"host,omitempty"`
	TXT      map[string]string `yaml:"txt,omitempty" json:"txt,omitempty"`
	VLAN     string            `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	Family   string            `yaml:"family,omitempty" json:"family,omitempty"`
}

// IsZero reports whether the selector constrains nothing (matches everything).
func (s Selector) IsZero() bool {
	return s.Service == "" && s.Instance == "" && s.Host == "" &&
		len(s.TXT) == 0 && s.VLAN == "" && s.Family == ""
}

// NamingRule maps discoveries matching Match to a DNS suffix (overriding the
// default). First matching rule wins.
type NamingRule struct {
	Match  Selector `yaml:"match" json:"match"`
	Suffix string   `yaml:"suffix" json:"suffix"`
}
