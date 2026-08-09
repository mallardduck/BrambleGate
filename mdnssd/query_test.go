package mdnssd

import (
	"testing"

	"github.com/miekg/dns"
)

func TestBuildQuery_BasicQuestion(t *testing.T) {
	msg := buildQuery("_http._tcp.local.", nil, false)

	if len(msg.Question) != 1 {
		t.Fatalf("Question count = %d, want 1", len(msg.Question))
	}
	q := msg.Question[0]
	if q.Name != "_http._tcp.local." {
		t.Errorf("Name = %q, want %q", q.Name, "_http._tcp.local.")
	}
	if q.Qtype != dns.TypePTR {
		t.Errorf("Qtype = %d, want TypePTR", q.Qtype)
	}
	if q.Qclass != dns.ClassINET {
		t.Errorf("Qclass = %#x, want ClassINET (QU bit unset)", q.Qclass)
	}
	if msg.Response {
		t.Error("Response = true, want false (this is a query)")
	}
	if len(msg.Answer) != 0 {
		t.Errorf("Answer count = %d, want 0 (no known answers passed)", len(msg.Answer))
	}
}

func TestBuildQuery_QUBitSet(t *testing.T) {
	msg := buildQuery("_http._tcp.local.", nil, true)

	q := msg.Question[0]
	if q.Qclass&quBit == 0 {
		t.Errorf("Qclass = %#x, want QU bit set", q.Qclass)
	}
	if q.Qclass&^quBit != dns.ClassINET {
		t.Errorf("Qclass low bits = %#x, want ClassINET", q.Qclass&^uint16(quBit))
	}
}

func TestBuildQuery_KnownAnswersEmbedded(t *testing.T) {
	known := []dns.RR{
		&dns.PTR{Hdr: rrHeader("_http._tcp.local.", dns.TypePTR, 60), Ptr: "Foo._http._tcp.local."},
	}

	msg := buildQuery("_http._tcp.local.", known, false)

	if len(msg.Answer) != 1 {
		t.Fatalf("Answer count = %d, want 1", len(msg.Answer))
	}
	if msg.Answer[0] != known[0] {
		t.Errorf("Answer[0] = %+v, want the known-answer record passed in", msg.Answer[0])
	}
}
