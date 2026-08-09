package localrecords

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// ddrQName is the fixed Discovery of Designated Resolvers query name (RFC 9462
// §5) — a special-use name under the reserved "resolver.arpa" domain, not tied
// to any zone the operator owns. Clients query it, over plain DNS, to learn
// this same server's encrypted upgrade paths (DoT/DoH/DoQ).
const ddrQName = "_dns.resolver.arpa."

// buildSVCBValue constructs the SvcParam pair for one DDR key=value entry.
// Only the keys configgen ever emits (alpn, port, dohpath — RFC 9460 §7, RFC
// 9461) are supported; anything else is a configgen/plugin version mismatch.
func buildSVCBValue(key, value string) (dns.SVCBKeyValue, error) {
	switch key {
	case "alpn":
		return &dns.SVCBAlpn{Alpn: strings.Split(value, ",")}, nil
	case "port":
		p, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("ddr svcparam port: %w", err)
		}
		return &dns.SVCBPort{Port: uint16(p)}, nil
	case "dohpath":
		return &dns.SVCBDoHPath{Template: value}, nil
	default:
		return nil, fmt.Errorf("unsupported ddr svcparam key %q", key)
	}
}

// buildSVCB assembles a fresh *dns.SVCB for rc (rc.rtype must be
// dns.TypeSVCB). Built fresh per answer, not cached/shared, since dns.RR
// values get mutated in place (e.g. Hdr.Name) by callers downstream.
func buildSVCB(rc *record, qname string, ttl uint32) (*dns.SVCB, error) {
	svcb := &dns.SVCB{
		Hdr:      dns.RR_Header{Name: qname, Rrtype: dns.TypeSVCB, Class: dns.ClassINET, Ttl: ttl},
		Priority: rc.ddrPriority,
		Target:   rc.ddrTarget,
	}
	for _, p := range rc.ddrParams {
		kv, err := buildSVCBValue(p.Key, p.Value)
		if err != nil {
			return nil, err
		}
		svcb.Value = append(svcb.Value, kv)
	}
	return svcb, nil
}
