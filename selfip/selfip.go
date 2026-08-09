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

	"github.com/mallardduck/BrambleGate/model"
)

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

	res := Result{PerVLAN: map[string]VLANAddrs{}}
	for _, v := range vlans {
		var nets []*net.IPNet
		for _, c := range v.CIDRs {
			_, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				continue
			}
			nets = append(nets, ipnet)
		}

		var va VLANAddrs
		for _, ip := range ips {
			for _, n := range nets {
				if !n.Contains(ip) {
					continue
				}
				if ip4 := ip.To4(); ip4 != nil {
					if va.V4 == "" {
						va.V4 = ip4.String()
					}
				} else if va.V6 == "" {
					va.V6 = ip.String()
				}
			}
		}
		if va.V4 != "" || va.V6 != "" {
			res.PerVLAN[v.Name] = va
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
