// Package localrecords is a CoreDNS plugin serving authoritative answers for the
// configured owned zone(s) (e.g. home.arpa), with per-VLAN split-horizon answers
// chosen by the client's source address. Its data is loaded once, at setup, from
// the JSON zone-data file configgen renders from records.yaml — the plugin never
// persists anything and does no runtime refresh (docs/plugins.md).
package localrecords

import (
	"context"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

// vlanMatch maps a VLAN name to the subnets that identify it. Evaluated in the
// order VLANs are declared in settings.yaml (first containing subnet wins).
type vlanMatch struct {
	name string
	nets []*net.IPNet
}

// override is a per-VLAN adjustment of a record's answer.
type override struct {
	value    string // "" => inherit record default
	ttl      uint32 // 0 => inherit record's effective TTL
	nxdomain bool   // true => no answer for this VLAN
}

// record is one (name,type) entry with its base value and per-VLAN overrides.
type record struct {
	rtype     uint16
	def       string // "" => no base answer (override-only record)
	ttl       uint32 // 0 => server default
	overrides map[string]override

	// ddrPriority/ddrTarget/ddrParams are set only when rtype is dns.TypeSVCB
	// (a DDR record — see ddr.go). SVCB records are never per-VLAN (the same
	// designated-resolver info applies to every client), so def/overrides are
	// unused for them.
	ddrPriority uint16
	ddrTarget   string
	ddrParams   []wireDDRParam
}

// LocalRecords is authoritative for Zones and answers from in-memory tables keyed
// by fully-qualified, lower-cased name, branching on the client's VLAN.
type LocalRecords struct {
	Next plugin.Handler

	Zones []string // normalized, e.g. "home.arpa."
	// FallthroughZones is the subset of Zones (e.g. the ACME domain, unlike the
	// fully-owned home.arpa) where a miss defers to Next instead of answering
	// NXDOMAIN/NODATA — so anything not explicitly declared here still resolves
	// via the real, public-authoritative DNS for that domain (docs/certificates.md).
	FallthroughZones []string
	defaultTTL       uint32
	vlans            []vlanMatch
	records          map[string][]*record
}

// isFallthroughZone reports whether zone (as returned by plugin.Zones.Matches,
// already one of the canonical Zones entries) defers to Next on a miss.
func (lr *LocalRecords) isFallthroughZone(zone string) bool {
	for _, z := range lr.FallthroughZones {
		if z == zone {
			return true
		}
	}
	return false
}

// Name implements plugin.Handler.
func (lr *LocalRecords) Name() string { return "localrecords" }

// ServeDNS answers authoritatively for names inside an owned zone and falls
// through for everything else. Within an owned zone it never falls through — a
// name with no answer for the client's VLAN is NXDOMAIN, so internal zone queries
// never leak to the upstream ad-block resolver (docs/plugins.md).
func (lr *LocalRecords) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := strings.ToLower(state.Name())

	zone := plugin.Zones(lr.Zones).Matches(qname)
	if zone == "" {
		return plugin.NextOrFailure(lr.Name(), lr.Next, ctx, w, r)
	}

	vlan := lr.matchVLAN(net.ParseIP(state.IP()))

	answers := lr.buildAnswers(qname, state.QType(), vlan)
	if len(answers) == 0 && lr.isFallthroughZone(zone) {
		return plugin.NextOrFailure(lr.Name(), lr.Next, ctx, w, r)
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	if len(answers) == 0 {
		// Does the name exist at all for this client (any type not suppressed)?
		if !lr.namePresent(qname, vlan) {
			m.Rcode = dns.RcodeNameError
			m.Ns = []dns.RR{lr.soa(zone)}
			_ = w.WriteMsg(m)
			return dns.RcodeNameError, nil
		}
		// Name exists for this client but has no record of the requested type.
		m.Ns = []dns.RR{lr.soa(zone)}
		_ = w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	}

	m.Answer = answers
	m.Extra = lr.ddrGlue(answers, vlan)
	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

// ddrGlue returns the target's own A/AAAA records for any DDR SVCB answers in
// answers, for the response's Additional section — so a client doesn't need a
// second query to resolve the designated resolver's address (RFC 9462 §4
// SHOULD). Resolved per the same client VLAN as the SVCB answer itself, since
// the target's address may also be split-horizon (e.g. the ACME domain).
func (lr *LocalRecords) ddrGlue(answers []dns.RR, vlan string) []dns.RR {
	var extra []dns.RR
	for _, rr := range answers {
		svcb, ok := rr.(*dns.SVCB)
		if !ok {
			continue
		}
		extra = append(extra, lr.buildAnswers(svcb.Target, dns.TypeA, vlan)...)
		extra = append(extra, lr.buildAnswers(svcb.Target, dns.TypeAAAA, vlan)...)
	}
	return extra
}

// matchVLAN returns the name of the first declared VLAN whose subnets contain ip,
// or "" if none match (in which case records use their defaults).
func (lr *LocalRecords) matchVLAN(ip net.IP) string {
	if ip == nil {
		return ""
	}
	for _, v := range lr.vlans {
		for _, n := range v.nets {
			if n.Contains(ip) {
				return v.name
			}
		}
	}
	return ""
}

// effective resolves a record's answer for a VLAN: the value, TTL, and whether it
// answers at all (false = suppressed by an nxdomain override or no value to give).
func (lr *LocalRecords) effective(rc *record, vlan string) (value string, ttl uint32, ok bool) {
	baseTTL := rc.ttl
	if baseTTL == 0 {
		baseTTL = lr.defaultTTL
	}
	if vlan != "" {
		if ov, has := rc.overrides[vlan]; has {
			if ov.nxdomain {
				return "", 0, false
			}
			v := ov.value
			if v == "" {
				v = rc.def
			}
			if v == "" {
				return "", 0, false
			}
			t := ov.ttl
			if t == 0 {
				t = baseTTL
			}
			return v, t, true
		}
	}
	if rc.def == "" {
		return "", 0, false
	}
	return rc.def, baseTTL, true
}

// namePresent reports whether any record for the name answers for this VLAN.
func (lr *LocalRecords) namePresent(qname, vlan string) bool {
	for _, rc := range lr.records[qname] {
		if rc.rtype == dns.TypeSVCB {
			return true // DDR records answer identically for every client
		}
		if _, _, ok := lr.effective(rc, vlan); ok {
			return true
		}
	}
	return false
}

// buildAnswers returns the answer RRs for a name/type from the client's VLAN. A
// CNAME aliases the whole name (returned regardless of qtype); for an address
// query whose target is in an owned zone, the resolved address records are
// appended so one query suffices.
func (lr *LocalRecords) buildAnswers(qname string, qtype uint16, vlan string) []dns.RR {
	for _, rc := range lr.records[qname] {
		if rc.rtype != dns.TypeCNAME {
			continue
		}
		target, ttl, ok := lr.effective(rc, vlan)
		if !ok {
			continue
		}
		out := []dns.RR{cnameRR(qname, target, ttl)}
		if qtype == dns.TypeA || qtype == dns.TypeAAAA {
			out = append(out, lr.buildAnswers(target, qtype, vlan)...)
		}
		return out
	}

	var out []dns.RR
	for _, rc := range lr.records[qname] {
		if rc.rtype != qtype {
			continue
		}
		if rc.rtype == dns.TypeSVCB {
			ttl := rc.ttl
			if ttl == 0 {
				ttl = lr.defaultTTL
			}
			svcb, err := buildSVCB(rc, qname, ttl)
			if err != nil {
				continue // validated at load; should not happen
			}
			out = append(out, svcb)
			continue
		}
		value, ttl, ok := lr.effective(rc, vlan)
		if !ok {
			continue
		}
		if rr := rrFor(qname, qtype, value, ttl); rr != nil {
			out = append(out, rr)
		}
	}
	return out
}

func rrFor(qname string, rtype uint16, value string, ttl uint32) dns.RR {
	hdr := dns.RR_Header{Name: qname, Rrtype: rtype, Class: dns.ClassINET, Ttl: ttl}
	switch rtype {
	case dns.TypeA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return nil
		}
		return &dns.A{Hdr: hdr, A: ip.To4()}
	case dns.TypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return nil
		}
		return &dns.AAAA{Hdr: hdr, AAAA: ip.To16()}
	default:
		return nil
	}
}

func cnameRR(qname, target string, ttl uint32) dns.RR {
	return &dns.CNAME{
		Hdr:    dns.RR_Header{Name: qname, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: ttl},
		Target: target,
	}
}

// soa synthesizes a minimal SOA for the authority section of NXDOMAIN/NODATA
// replies, so negative answers cache correctly and dig output is well-formed.
func (lr *LocalRecords) soa(zone string) dns.RR {
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: lr.defaultTTL},
		Ns:      "ns." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  1,
		Refresh: 7200,
		Retry:   1800,
		Expire:  86400,
		Minttl:  lr.defaultTTL,
	}
}
