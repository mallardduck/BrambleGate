// Package localrecords is a CoreDNS plugin serving authoritative answers for the
// configured owned zone(s) (e.g. home.arpa). Its data is parsed once, at setup,
// from the inline Corefile block that configgen renders from records.yaml — the
// plugin never reads a file or persists anything at runtime (docs/plugins.md).
package localrecords

import (
	"context"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

// entry is one record value for a name (Phase 2: a single value per (name,type);
// per-VLAN split-horizon variants arrive in Phase 3).
type entry struct {
	rtype uint16
	value string
}

// LocalRecords is authoritative for Zones and answers from an in-memory table
// keyed by fully-qualified, lower-cased name.
type LocalRecords struct {
	Next plugin.Handler

	Zones   []string // normalized, e.g. "home.arpa."
	TTL     uint32
	records map[string][]entry
}

// Name implements plugin.Handler.
func (lr *LocalRecords) Name() string { return "localrecords" }

// ServeDNS answers authoritatively for names inside an owned zone and falls
// through for everything else. Within an owned zone it never falls through — an
// unknown name is NXDOMAIN, so internal zone queries never leak to the upstream
// ad-block resolver (docs/plugins.md).
func (lr *LocalRecords) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := strings.ToLower(state.Name())

	zone := plugin.Zones(lr.Zones).Matches(qname)
	if zone == "" {
		return plugin.NextOrFailure(lr.Name(), lr.Next, ctx, w, r)
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	entries := lr.records[qname]
	if len(entries) == 0 {
		// Name does not exist in an owned zone → authoritative NXDOMAIN.
		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{lr.soa(zone)}
		_ = w.WriteMsg(m)
		return dns.RcodeNameError, nil
	}

	answers := lr.answer(qname, state.QType(), entries)
	if len(answers) == 0 {
		// Name exists but has no record of the requested type → NODATA.
		m.Ns = []dns.RR{lr.soa(zone)}
		_ = w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	}

	m.Answer = answers
	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

// answer builds the answer RRs for a name. A CNAME aliases the whole name, so it
// is returned regardless of qtype; when the query is for an address type and the
// CNAME target lives in an owned zone, the resolved address records are appended
// so a single query is sufficient.
func (lr *LocalRecords) answer(qname string, qtype uint16, entries []entry) []dns.RR {
	for _, e := range entries {
		if e.rtype == dns.TypeCNAME {
			target := dns.CanonicalName(e.value)
			out := []dns.RR{cnameRR(qname, target, lr.TTL)}
			if qtype == dns.TypeA || qtype == dns.TypeAAAA {
				out = append(out, lr.answer(target, qtype, lr.records[target])...)
			}
			return out
		}
	}

	var out []dns.RR
	for _, e := range entries {
		if e.rtype != qtype {
			continue
		}
		if rr := rrFor(qname, e, lr.TTL); rr != nil {
			out = append(out, rr)
		}
	}
	return out
}

func rrFor(qname string, e entry, ttl uint32) dns.RR {
	hdr := dns.RR_Header{Name: qname, Rrtype: e.rtype, Class: dns.ClassINET, Ttl: ttl}
	switch e.rtype {
	case dns.TypeA:
		ip := net.ParseIP(e.value)
		if ip == nil || ip.To4() == nil {
			return nil
		}
		return &dns.A{Hdr: hdr, A: ip.To4()}
	case dns.TypeAAAA:
		ip := net.ParseIP(e.value)
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
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: lr.TTL},
		Ns:      "ns." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  1,
		Refresh: 7200,
		Retry:   1800,
		Expire:  86400,
		Minttl:  lr.TTL,
	}
}
