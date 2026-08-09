package mdnssd

import "github.com/miekg/dns"

// quBit is RFC 6762 §5.4's unicast-response bit: the top bit of a question's
// qclass field, set to ask the responder to reply via unicast rather than
// multicast (used only for a browse's first query).
const quBit = 1 << 15

// buildQuery constructs an mDNS PTR query for qname. knownAnswers (RFC 6762
// §7.1) are embedded in the Answer section so responders can suppress
// records the querier already has; pass nil for none. unicast sets the QU
// bit (§5.4), appropriate only for the first query of a browse.
func buildQuery(qname string, knownAnswers []dns.RR, unicast bool) *dns.Msg {
	msg := new(dns.Msg)
	msg.Response = false

	qclass := uint16(dns.ClassINET)
	if unicast {
		qclass |= quBit
	}
	msg.Question = []dns.Question{{Name: qname, Qtype: dns.TypePTR, Qclass: qclass}}
	msg.Answer = knownAnswers

	return msg
}
