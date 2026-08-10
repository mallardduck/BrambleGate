// Package vlanmatch matches an IP address against a set of VLAN CIDR
// declarations — the "which VLAN is this address in" question that
// localrecords, mdnsbridge, selfip, and mdnscfg previously each answered with
// their own independent net.ParseCIDR loop (see dev-docs/query-log.md).
//
// Table is a generic, reusable, side-effect-free primitive: it can be built
// from any []VLAN, for any purpose — selfip, for instance, builds its own
// throwaway Tables for self-address/candidate-detection concerns that have
// nothing to do with the app's actual configured VLANs. Current/SetCurrent
// are a distinct, separate concept: the single process-wide Table
// representing the app's real configured VLANs (settings.yaml's VLANs
// list), which localrecords/mdnsbridge/querylog read as their source of
// truth at request time instead of each holding their own copy. Do not
// conflate the two — a function taking a []VLAN parameter should stay
// generic and never silently reach for Current().
package vlanmatch

import "net"

// VLAN is the minimal shape vlanmatch needs: a name and its declared CIDRs.
// Deliberately not model.VLAN — see dev-docs/repo-layout.md's "why separate
// modules": CoreDNS-chain plugins (localrecords, mdnsbridge) depend on
// nothing but coredns/miekg/dns/pluginreg so they stay independently
// extractable, and importing vlanmatch must not pull model in behind their
// backs. Callers that already depend on model (internal/cli, configgen,
// mdnscfg) do a trivial one-line conversion from []model.VLAN before calling
// in.
type VLAN struct {
	Name  string
	CIDRs []string
}

type vlanNets struct {
	name string
	nets []*net.IPNet
}

// Table matches an IP against a set of VLANs' declared CIDRs. The zero Table
// matches nothing (equivalent to "no VLANs declared").
type Table struct {
	vlans []vlanNets
}

// NewTable parses vlans' CIDRs once. A malformed CIDR string is skipped
// rather than erroring — callers are expected to have already validated
// VLAN CIDRs (configgen/validate.go's validateVLANs runs at render time), so
// this never needs a second opinion on well-formedness.
func NewTable(vlans []VLAN) Table {
	t := Table{vlans: make([]vlanNets, 0, len(vlans))}
	for _, v := range vlans {
		vn := vlanNets{name: v.Name}
		for _, c := range v.CIDRs {
			if _, n, err := net.ParseCIDR(c); err == nil {
				vn.nets = append(vn.nets, n)
			}
		}
		t.vlans = append(t.vlans, vn)
	}
	return t
}

// Lookup returns the name of the first declared VLAN whose CIDRs contain ip,
// in the order VLANs were passed to NewTable (settings.yaml order — first
// containing subnet wins, the same split-horizon precedence documented in
// dev-docs/plugins.md). ok is false if ip is nil or matches no declared
// VLAN.
func (t Table) Lookup(ip net.IP) (name string, ok bool) {
	if ip == nil {
		return "", false
	}
	for _, v := range t.vlans {
		for _, n := range v.nets {
			if n.Contains(ip) {
				return v.name, true
			}
		}
	}
	return "", false
}
