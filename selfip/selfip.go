// Package selfip detects which of this machine's own local IPs correspond to
// each declared VLAN, so a record (e.g. the ACME domain's) can be answered
// with a per-VLAN LAN address without the user typing it in by hand. This is
// reliable in a macvlan-style deployment (one Docker network per VLAN, so the
// container's own interfaces carry real per-VLAN LAN IPs) and degrades to
// "nothing detected" under a plain bridge/port-mapped deployment, where the
// container only sees Docker's internal bridge IP (dev-docs/certificates.md).
//
// A sibling library module (not internal/), matching model/engine/mdnssd's
// layout, since both configgen and the root module (internal/gui) need to
// import it — see docs/repo-layout.md.
package selfip

import (
	"net"
	"strings"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// toVLANMatch converts model.VLAN to vlanmatch's own minimal shape — a
// one-line adapter, not a reimplementation of CIDR parsing/matching, so
// selfip stays on the single vlanmatch.Table primitive like every other VLAN
// CIDR consumer (dev-docs/query-log.md). selfip builds its own throwaway
// Tables here rather than reading vlanmatch.Current(): Detect/Candidates are
// pure functions over whatever []model.VLAN the caller passes in, not
// readers of the app's global configured-VLANs state.
func toVLANMatch(vlans []model.VLAN) []vlanmatch.VLAN {
	out := make([]vlanmatch.VLAN, len(vlans))
	for i, v := range vlans {
		out[i] = vlanmatch.VLAN{Name: v.Name, CIDRs: v.CIDRs}
	}
	return out
}

// VLANAddrs is the local IP(s) found for one VLAN. Either field may be empty.
type VLANAddrs struct {
	V4 string
	V6 string
}

// Result is the outcome of detection: local addresses per matched VLAN, plus a
// best-effort Primary fallback for clients whose source doesn't match any
// declared VLAN. A zero Result means nothing usable was found.
type Result struct {
	PerVLAN map[string]VLANAddrs // keyed by VLAN name; only VLANs with a match
	Primary VLANAddrs
}

// Detect matches addrs (in the shape net.InterfaceAddrs returns) against each
// VLAN's declared CIDRs. It is pure and side-effect free so it can be unit
// tested without touching real interfaces — see DetectLive for the production
// entry point.
func Detect(vlans []model.VLAN, addrs []net.Addr) Result {
	ips := extractIPs(addrs)
	tbl := vlanmatch.NewTable(toVLANMatch(vlans))

	res := Result{PerVLAN: map[string]VLANAddrs{}}
	for _, ip := range ips {
		if name, ok := tbl.Lookup(ip); ok {
			va := res.PerVLAN[name]
			if ip4 := ip.To4(); ip4 != nil {
				if va.V4 == "" {
					va.V4 = ip4.String()
				}
			} else if va.V6 == "" {
				va.V6 = ip.String()
			}
			res.PerVLAN[name] = va
		}
	}

	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			if res.Primary.V4 == "" {
				res.Primary.V4 = ip4.String()
			}
		} else if res.Primary.V6 == "" {
			res.Primary.V6 = ip.String()
		}
	}

	return res
}

// DetectLive calls Detect against the real local interfaces. It never errors —
// if enumeration fails (unusual) it returns a zero Result, and callers degrade
// to "nothing detected" (the same outcome as a bridge-mode deployment) rather
// than failing config render over it.
func DetectLive(vlans []model.VLAN) Result {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return Result{PerVLAN: map[string]VLANAddrs{}}
	}
	return Detect(vlans, addrs)
}

// Candidate is a locally-attached network not yet covered by any declared
// VLAN — a suggestion for the Settings page to offer as "add this as a VLAN",
// not something BrambleGate ever declares on its own (it isn't the authority
// for VLANs — see model.VLAN).
type Candidate struct {
	CIDR      string // network address in CIDR form, e.g. "192.168.30.0/23"
	SampleIP  string // one address seen in that network, for display
	Suggested string // a generated, editable default name (e.g. "net-192-168-30")
}

// Candidates finds locally-attached networks (from addrs, in the shape
// net.InterfaceAddrs returns) that aren't already covered by any of existing's
// declared VLAN CIDRs — one Candidate per distinct network. Only *net.IPNet
// entries carry a usable prefix length (the *net.IPAddr shape extractIPs also
// accepts has no mask, so it can't be turned into a CIDR and is skipped here).
func Candidates(existing []model.VLAN, addrs []net.Addr) []Candidate {
	tbl := vlanmatch.NewTable(toVLANMatch(existing))

	seen := map[string]bool{}
	var out []Candidate
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP == nil {
			continue
		}
		ip := ipnet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			continue
		}

		if _, ok := tbl.Lookup(ip); ok {
			continue
		}

		network := &net.IPNet{IP: ip.Mask(ipnet.Mask), Mask: ipnet.Mask}
		cidr := network.String()
		if seen[cidr] {
			continue
		}
		seen[cidr] = true
		out = append(out, Candidate{
			CIDR:      cidr,
			SampleIP:  ip.String(),
			Suggested: suggestName(network.IP),
		})
	}
	return out
}

// CandidatesLive calls Candidates against the real local interfaces. Never
// errors — enumeration failure just yields no candidates.
func CandidatesLive(existing []model.VLAN) []Candidate {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	return Candidates(existing, addrs)
}

// suggestName turns a network address into a readable, editable default VLAN
// name, e.g. 192.168.30.0 -> "net-192-168-30". Purely a starting point for the
// user to rename — BrambleGate has no opinion on what a network is actually
// called (mirrors the user's own gear, per model.VLAN's doc comment).
func suggestName(networkIP net.IP) string {
	s := networkIP.String()
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, ":", "-")
	return "net-" + s
}

// extractIPs pulls usable IPs out of addrs, skipping loopback/link-local/
// unspecified addresses (net.InterfaceAddrs returns *net.IPNet entries; the
// *net.IPAddr case is handled too since it's the other concrete type the net
// package uses for this shape).
func extractIPs(addrs []net.Addr) []net.IP {
	var ips []net.IP
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}
