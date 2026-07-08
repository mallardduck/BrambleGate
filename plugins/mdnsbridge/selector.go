package mdnsbridge

import (
	"net"
	"strings"
)

// Selector matches a discovered mDNS-SD entry. Every set field must match (AND);
// an unset field is a wildcard. Values are case-insensitive globs (* and ?). A
// zero Selector matches everything. It is the one primitive used for auto-publish
// filtering, naming overrides, and promoted-record targeting (docs/plugins.md).
type Selector struct {
	Service  string            // e.g. "_airplay._tcp", "_*._tcp"
	Instance string            // instance label, e.g. "Living Room*"
	Host     string            // ".local" hostname
	TXT      map[string]string // TXT key -> value glob; all keys must match
	VLAN     string            // source IP in this VLAN's CIDRs
	Family   string            // "ipv4" | "ipv6" | ""
}

// IsZero reports whether the selector constrains nothing (matches everything).
func (s Selector) IsZero() bool {
	return s.Service == "" && s.Instance == "" && s.Host == "" &&
		len(s.TXT) == 0 && s.VLAN == "" && s.Family == ""
}

// Match reports whether e satisfies the selector. vlans maps VLAN name to its
// CIDRs for the VLAN condition.
func (s Selector) Match(e Entry, vlans map[string][]*net.IPNet) bool {
	if s.Service != "" && !globMatch(s.Service, e.Service) {
		return false
	}
	if s.Instance != "" && !globMatch(s.Instance, e.Instance) {
		return false
	}
	// Host is compared trailing-dot-insensitively (printer.local == printer.local.).
	if s.Host != "" && !globMatch(strings.TrimSuffix(s.Host, "."), strings.TrimSuffix(e.Host, ".")) {
		return false
	}
	for k, want := range s.TXT {
		got, ok := e.TXT[k]
		if !ok || !globMatch(want, got) {
			return false
		}
	}
	switch strings.ToLower(s.Family) {
	case "ipv4":
		if len(e.IPv4) == 0 {
			return false
		}
	case "ipv6":
		if len(e.IPv6) == 0 {
			return false
		}
	}
	if s.VLAN != "" && !entryInVLAN(e, vlans[s.VLAN]) {
		return false
	}
	return true
}

func entryInVLAN(e Entry, nets []*net.IPNet) bool {
	if len(nets) == 0 {
		return false
	}
	for _, s := range append(append([]string{}, e.IPv4...), e.IPv6...) {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// SelectorSet is a list of selectors combined with OR.
type SelectorSet []Selector

// MatchAny reports whether any selector in the set matches e. An empty set
// matches nothing.
func (ss SelectorSet) MatchAny(e Entry, vlans map[string][]*net.IPNet) bool {
	for _, s := range ss {
		if s.Match(e, vlans) {
			return true
		}
	}
	return false
}

// globMatch is a case-insensitive glob match supporting * (any run) and ? (one
// char). No path semantics — the whole string must match.
func globMatch(pattern, s string) bool {
	return glob(strings.ToLower(pattern), strings.ToLower(s))
}

func glob(p, s string) bool {
	// Iterative matcher with backtracking on '*'.
	star := -1
	var ss int
	i, j := 0, 0
	for j < len(s) {
		switch {
		case i < len(p) && (p[i] == '?' || p[i] == s[j]):
			i++
			j++
		case i < len(p) && p[i] == '*':
			star = i
			ss = j
			i++
		case star >= 0:
			i = star + 1
			ss++
			j = ss
		default:
			return false
		}
	}
	for i < len(p) && p[i] == '*' {
		i++
	}
	return i == len(p)
}
