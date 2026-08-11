package vlancache

import (
	"time"

	"github.com/miekg/dns"
)

// entry is one cached DNS response, independent of how it was keyed (VLAN
// bucket or RFC 7871 scope prefix — see store.go).
type entry struct {
	rcode              int
	authenticatedData  bool
	recursionAvailable bool
	answer             []dns.RR
	ns                 []dns.RR
	extra              []dns.RR

	origTTL time.Duration
	stored  time.Time
}

// newEntry snapshots m for caching. OPT records are hop-by-hop (RFC 6891)
// and are never retained.
func newEntry(m *dns.Msg, now time.Time, ttl time.Duration) *entry {
	return &entry{
		rcode:              m.Rcode,
		authenticatedData:  m.AuthenticatedData,
		recursionAvailable: m.RecursionAvailable,
		answer:             stripOPT(m.Answer),
		ns:                 stripOPT(m.Ns),
		extra:              stripOPT(m.Extra),
		origTTL:            ttl,
		stored:             now,
	}
}

func stripOPT(rrs []dns.RR) []dns.RR {
	out := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		if rr.Header().Rrtype == dns.TypeOPT {
			continue
		}
		out = append(out, rr)
	}
	return out
}

// remaining is the entry's remaining lifetime as of now; <= 0 means expired.
func (e *entry) remaining(now time.Time) time.Duration {
	return e.origTTL - now.Sub(e.stored)
}

// toMsg tailors the cached response into a reply for req, rewriting RR TTLs
// to the entry's remaining lifetime. do/ad mirror the stock cache plugin's
// AD-bit handling: DNSSEC data can't be marked authenticated for a requester
// that didn't ask for DNSSEC and didn't itself set AD (RFC 6840 5.7-5.8).
func (e *entry) toMsg(req *dns.Msg, now time.Time, do, ad bool) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.Rcode = e.rcode
	m.RecursionAvailable = e.recursionAvailable
	m.AuthenticatedData = e.authenticatedData
	if !do && !ad {
		m.AuthenticatedData = false
	}

	rem := e.remaining(now)
	if rem < 0 {
		rem = 0
	}
	ttl := uint32(rem / time.Second)
	m.Answer = copyWithTTL(e.answer, ttl)
	m.Ns = copyWithTTL(e.ns, ttl)
	m.Extra = copyWithTTL(e.extra, ttl)
	return m
}

func copyWithTTL(rrs []dns.RR, ttl uint32) []dns.RR {
	out := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		c := dns.Copy(rr)
		c.Header().Ttl = ttl
		out[i] = c
	}
	return out
}
