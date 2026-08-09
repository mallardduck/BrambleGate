package mdnssd

import (
	"time"

	"github.com/miekg/dns"
)

// knownRecord is a minimal view of a cached record needed to decide whether
// it qualifies as a "known answer" for an outbound query.
type knownRecord struct {
	RR       dns.RR        // the record to potentially re-send
	Question string        // the query name this record answers
	TTL      time.Duration // original TTL as announced
	Elapsed  time.Duration // time since it was last (re)stored
}

// knownAnswers implements RFC 6762 §7.1: records offered to a responder as
// "already known" so it can skip re-sending them. Only records answering
// question and with MORE than half their original TTL remaining qualify —
// a record past the halfway point is close enough to expiring that claiming
// it as known risks suppressing an answer the querier actually needs.
// Qualifying records are returned with their TTL adjusted down to the
// remaining time, as the RFC requires for known-answer lists.
func knownAnswers(records []knownRecord, question string) []dns.RR {
	var out []dns.RR
	for _, rec := range records {
		if rec.Question != question {
			continue
		}
		remaining := rec.TTL - rec.Elapsed
		if remaining*2 <= rec.TTL { // must be strictly more than half remaining
			continue
		}
		rr := dns.Copy(rec.RR)
		rr.Header().Ttl = uint32(remaining.Seconds())
		out = append(out, rr)
	}
	return out
}
