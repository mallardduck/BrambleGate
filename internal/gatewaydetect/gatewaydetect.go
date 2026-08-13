// Package gatewaydetect guesses each declared VLAN's gateway (router) IP —
// the PTR tier's default target when client_names.ptr_upstream isn't set
// explicitly (dev-docs/client-names.md). Sending PTR queries to
// upstream_dns is usually pointless (a public ad-block resolver has no idea
// what's on the LAN), but the router almost always both knows local reverse
// names (via its own DHCP leases) and doubles as the LAN's DNS server, so
// it's a much better default target — the same assumption Pi-hole's own
// client-name resolution leans on.
//
// A tiered strategy: read the real OS routing table first (most accurate,
// OS-specific — see routes_linux.go/routes_other.go), falling back to the
// "network + 1" convention (e.g. 192.168.10.0/24 -> 192.168.10.1) for any
// VLAN the routing table didn't resolve.
package gatewaydetect

import (
	"net"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// Result is the best-guess gateway per VLAN, plus a Primary fallback for
// clients whose source matched no declared VLAN (e.g. no VLANs configured
// at all — a single flat home network, still worth a real default gateway
// guess). Mirrors internal/configgen/selfip.Result's shape.
type Result struct {
	PerVLAN map[string]string // VLAN name -> gateway IP; only VLANs with a guess
	Primary string            // best-effort default gateway, "" if none found
}

// ifaceIPs is interface name -> its local IPs (net.InterfaceAddrs loses
// interface identity, so DetectLive uses net.Interfaces()+Addrs() instead —
// gatewayFor needs to know which interface a local IP belongs to, to look
// up that interface's own default-route gateway).
type ifaceIPs map[string][]net.IP

// detect is the pure core: given each interface's locally-assigned IPs, each
// interface's OS-reported default-route gateway (nil/missing when unknown —
// see routes_other.go's no-op), and the declared VLANs, produce one gateway
// guess per VLAN. Kept side-effect free so it's unit testable without real
// interfaces/routing tables; DetectLive is the production entry point.
func detect(vlans []model.VLAN, ifaces ifaceIPs, gateways map[string]net.IP) Result {
	tbl := vlanmatch.NewTable(toVLANMatch(vlans))
	res := Result{PerVLAN: map[string]string{}}

	// Tier 1: the real default-route gateway, via whichever interface's
	// local IP falls inside the VLAN's declared CIDR(s).
	for iface, ips := range ifaces {
		gw, ok := gateways[iface]
		if !ok || gw == nil {
			continue
		}
		for _, ip := range ips {
			if name, ok := tbl.Lookup(ip); ok {
				if _, already := res.PerVLAN[name]; !already {
					res.PerVLAN[name] = gw.String()
				}
			}
		}
		if res.Primary == "" {
			res.Primary = gw.String()
		}
	}

	// Tier 2: "network + 1" heuristic for any VLAN tier 1 didn't resolve.
	// No heuristic equivalent for Primary — there's no specific CIDR to
	// apply it to when nothing matched a declared VLAN.
	for _, v := range vlans {
		if _, ok := res.PerVLAN[v.Name]; ok {
			continue
		}
		if gw := heuristicGateway(v.CIDRs); gw != "" {
			res.PerVLAN[v.Name] = gw
		}
	}

	return res
}

// heuristicGateway guesses a CIDR's gateway as its first usable host address
// (network address + 1) — the overwhelmingly common home-router convention.
// IPv4-only: there's no equivalent "+1" convention for an IPv6 prefix.
func heuristicGateway(cidrs []string) string {
	for _, c := range cidrs {
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		gw := make(net.IP, len(ip4))
		copy(gw, ip4)
		gw[3]++
		return gw.String()
	}
	return ""
}

// toVLANMatch converts model.VLAN to vlanmatch's own minimal shape — the
// same one-line adapter internal/configgen/selfip uses, not a
// reimplementation of CIDR parsing/matching.
func toVLANMatch(vlans []model.VLAN) []vlanmatch.VLAN {
	out := make([]vlanmatch.VLAN, len(vlans))
	for i, v := range vlans {
		out[i] = vlanmatch.VLAN{Name: v.Name, CIDRs: v.CIDRs}
	}
	return out
}

// DetectLive calls detect against the real local interfaces and the real
// OS-specific default-route table. Never errors — enumeration/parse
// failures just degrade to "nothing detected via tier 1," falling through to
// the heuristic (or, on an unsupported OS, to the heuristic for every VLAN —
// see routes_other.go).
func DetectLive(vlans []model.VLAN) Result {
	ifaces := ifaceIPs{}
	if list, err := net.Interfaces(); err == nil {
		for _, iface := range list {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
					continue
				}
				ifaces[iface.Name] = append(ifaces[iface.Name], ip)
			}
		}
	}
	return detect(vlans, ifaces, defaultGateways())
}
