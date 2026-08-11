package mdnsbridge

import (
	"context"
	"net"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/plugins/querylog"
)

// DefaultAnswerTTL is the TTL on answers synthesized from discovered entries.
// Short, because mDNS liveness changes quickly.
const DefaultAnswerTTL = 60

// MDNSBridge resolves a query against the shared discovery Table. If the name is
// the table's to answer (a promoted binding or a live published name) it answers
// A/AAAA — or NODATA when the device is currently absent — otherwise it falls
// through. It is chained ahead of localrecords, which remains the authoritative
// home.arpa NXDOMAIN emitter for names mDNS does not own (docs/plugins.md).
type MDNSBridge struct {
	Next  plugin.Handler
	Table *Table
}

func (m *MDNSBridge) Name() string { return "mdnsbridge" }

func (m *MDNSBridge) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	ipv4, ipv6, owned := m.Table.Resolve(state.Name())
	if !owned {
		return plugin.NextOrFailure(m.Name(), m.Next, ctx, w, r)
	}

	// The name is ours to answer authoritatively (present → addresses, absent →
	// NODATA). Only A/AAAA carry addresses; any other type on an owned name is
	// NODATA.
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true
	msg.Answer = answersFor(state.Name(), state.QType(), ipv4, ipv6)
	if e := querylog.FromContext(ctx); e != nil {
		e.Source = "mdnsbridge"
		e.Verdict = "local"
	}
	_ = w.WriteMsg(msg)
	return dns.RcodeSuccess, nil
}

func answersFor(qname string, qtype uint16, ipv4, ipv6 []string) []dns.RR {
	var out []dns.RR
	if qtype == dns.TypeA {
		for _, s := range ipv4 {
			if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
				out = append(out, &dns.A{
					Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: DefaultAnswerTTL},
					A:   ip.To4(),
				})
			}
		}
	}
	if qtype == dns.TypeAAAA {
		for _, s := range ipv6 {
			if ip := net.ParseIP(s); ip != nil && ip.To4() == nil {
				out = append(out, &dns.AAAA{
					Hdr:  dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: DefaultAnswerTTL},
					AAAA: ip.To16(),
				})
			}
		}
	}
	return out
}
