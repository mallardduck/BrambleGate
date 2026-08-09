package mdnssd

import (
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// ptrRecord is a parsed PTR record: Name is the question name being answered
// (e.g. a service type "_http._tcp.local." or the meta-query name), Ptr is
// the target it points at (a service instance name, or — for the meta-query
// — a discovered service type).
type ptrRecord struct {
	Name string
	Ptr  string
	TTL  time.Duration
}

// srvRecord is a parsed SRV record for a service instance.
type srvRecord struct {
	Name   string
	Target string
	Port   int
	TTL    time.Duration
}

// txtRecord is a parsed TXT record, decoded from "key=value" segments.
// Segments without an "=" are ignored.
type txtRecord struct {
	Name string
	Text map[string]string
	TTL  time.Duration
}

// aRecord is a parsed IPv4 address record.
type aRecord struct {
	Name string
	IP   net.IP
	TTL  time.Duration
}

// aaaaRecord is a parsed IPv6 address record.
type aaaaRecord struct {
	Name string
	IP   net.IP
	TTL  time.Duration
}

// parsedAnswers groups the record types mdnssd understands out of a raw
// mDNS message. Anything else (NSEC, OPT, ...) is ignored.
type parsedAnswers struct {
	PTR  []ptrRecord
	SRV  []srvRecord
	TXT  []txtRecord
	A    []aRecord
	AAAA []aaaaRecord
}

// parseAnswers scans msg's Answer, Ns, and Extra sections — mDNS responses
// commonly spread PTR/SRV/TXT/A/AAAA records across all three rather than
// using Answer alone (mirrors how dnssd's cache.go filterRecords does it).
func parseAnswers(msg *dns.Msg) parsedAnswers {
	var out parsedAnswers

	all := make([]dns.RR, 0, len(msg.Answer)+len(msg.Ns)+len(msg.Extra))
	all = append(all, msg.Answer...)
	all = append(all, msg.Ns...)
	all = append(all, msg.Extra...)

	for _, rr := range all {
		ttl := time.Duration(rr.Header().Ttl) * time.Second
		switch r := rr.(type) {
		case *dns.PTR:
			out.PTR = append(out.PTR, ptrRecord{Name: r.Hdr.Name, Ptr: r.Ptr, TTL: ttl})
		case *dns.SRV:
			out.SRV = append(out.SRV, srvRecord{Name: r.Hdr.Name, Target: r.Target, Port: int(r.Port), TTL: ttl})
		case *dns.TXT:
			out.TXT = append(out.TXT, txtRecord{Name: r.Hdr.Name, Text: parseTXT(r.Txt), TTL: ttl})
		case *dns.A:
			out.A = append(out.A, aRecord{Name: r.Hdr.Name, IP: r.A, TTL: ttl})
		case *dns.AAAA:
			out.AAAA = append(out.AAAA, aaaaRecord{Name: r.Hdr.Name, IP: r.AAAA, TTL: ttl})
		}
	}

	return out
}

func parseTXT(segments []string) map[string]string {
	text := make(map[string]string)
	for _, s := range segments {
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			continue
		}
		text[key] = value
	}
	return text
}
