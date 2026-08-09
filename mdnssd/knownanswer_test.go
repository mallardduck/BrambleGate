package mdnssd

import (
	"testing"
	"time"

	"github.com/miekg/dns"
)

func newKnownPTR(question string, elapsed, ttl time.Duration) knownRecord {
	return knownRecord{
		RR:       &dns.PTR{Hdr: rrHeader(question, dns.TypePTR, uint32(ttl.Seconds())), Ptr: "Foo." + question},
		Question: question,
		TTL:      ttl,
		Elapsed:  elapsed,
	}
}

func TestKnownAnswers_IncludesRecordWithMostTTLRemaining(t *testing.T) {
	rec := newKnownPTR("_http._tcp.local.", 10*time.Second, 100*time.Second) // 90% remaining

	got := knownAnswers([]knownRecord{rec}, "_http._tcp.local.")

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if ttl := got[0].Header().Ttl; ttl != 90 {
		t.Errorf("adjusted TTL = %d, want 90 (remaining seconds)", ttl)
	}
}

func TestKnownAnswers_ExcludesRecordAtOrBelowHalfTTL(t *testing.T) {
	// RFC 6762 §7.1: only include if MORE than half the original TTL remains.
	atHalf := newKnownPTR("_http._tcp.local.", 50*time.Second, 100*time.Second)   // exactly 50% remaining
	pastHalf := newKnownPTR("_http._tcp.local.", 60*time.Second, 100*time.Second) // 40% remaining

	got := knownAnswers([]knownRecord{atHalf, pastHalf}, "_http._tcp.local.")

	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 — neither record has >50%% TTL remaining", len(got))
	}
}

func TestKnownAnswers_FiltersByQuestion(t *testing.T) {
	http := newKnownPTR("_http._tcp.local.", 10*time.Second, 100*time.Second)
	ssh := newKnownPTR("_ssh._tcp.local.", 10*time.Second, 100*time.Second)

	got := knownAnswers([]knownRecord{http, ssh}, "_http._tcp.local.")

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Header().Name != "_http._tcp.local." {
		t.Errorf("Name = %q, want %q", got[0].Header().Name, "_http._tcp.local.")
	}
}

func TestKnownAnswers_EmptyWhenNoRecords(t *testing.T) {
	got := knownAnswers(nil, "_http._tcp.local.")
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}
