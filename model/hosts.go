package model

import "strings"

// HostSet mirrors /config/hosts.yaml (dev-docs/static-hosts.md) — a
// deliberately dumb escape hatch, distinct from RecordSet: no type/ttl/
// vlan_overrides, one canonical name (or a few aliases) per IP, answering
// any domain rather than just zones BrambleGate owns.
type HostSet struct {
	Hosts []Host `yaml:"hosts" json:"hosts"`
}

// Host is one static IP-to-name override. Hostname is the single canonical
// name — what plugins/clientnames (Phase 9) reads for display and what its
// "promote" action writes — Aliases covers any additional names that should
// resolve to the same IP without being ambiguous about which one is "the"
// name for that client (dev-docs/static-hosts.md's "hostname vs aliases"
// section).
type Host struct {
	IP       string   `yaml:"ip" json:"ip"`
	Hostname string   `yaml:"hostname" json:"hostname"`
	Aliases  []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
}

// Names returns Hostname followed by every Aliases entry — the full set of
// names this Host answers for, in the order configgen renders them on the
// hosts-file line.
func (h Host) Names() []string {
	return append([]string{h.Hostname}, h.Aliases...)
}

// NormalizedName returns name as a fully-qualified, lower-cased DNS name
// (trailing dot) — the same convention as Record.NormalizedName, used for
// dedup/shadow checks against records.yaml.
func NormalizedHostName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}
