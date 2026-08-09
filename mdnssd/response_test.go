package mdnssd

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func rrHeader(name string, rrtype uint16, ttl uint32) dns.RR_Header {
	return dns.RR_Header{Name: name, Rrtype: rrtype, Class: dns.ClassINET, Ttl: ttl}
}

func TestParseAnswers_PTR(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.PTR{Hdr: rrHeader("_http._tcp.local.", dns.TypePTR, 120), Ptr: "Foo._http._tcp.local."},
	}

	got := parseAnswers(msg)

	if len(got.PTR) != 1 {
		t.Fatalf("PTR count = %d, want 1", len(got.PTR))
	}
	want := ptrRecord{Name: "_http._tcp.local.", Ptr: "Foo._http._tcp.local.", TTL: 120 * time.Second}
	if got.PTR[0] != want {
		t.Errorf("PTR[0] = %+v, want %+v", got.PTR[0], want)
	}
}

func TestParseAnswers_SRV(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.SRV{Hdr: rrHeader("Foo._http._tcp.local.", dns.TypeSRV, 120), Target: "foo.local.", Port: 8080},
	}

	got := parseAnswers(msg)

	if len(got.SRV) != 1 {
		t.Fatalf("SRV count = %d, want 1", len(got.SRV))
	}
	want := srvRecord{Name: "Foo._http._tcp.local.", Target: "foo.local.", Port: 8080, TTL: 120 * time.Second}
	if got.SRV[0] != want {
		t.Errorf("SRV[0] = %+v, want %+v", got.SRV[0], want)
	}
}

func TestParseAnswers_TXT(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.TXT{Hdr: rrHeader("Foo._http._tcp.local.", dns.TypeTXT, 120), Txt: []string{"path=/", "version=2", "malformed"}},
	}

	got := parseAnswers(msg)

	if len(got.TXT) != 1 {
		t.Fatalf("TXT count = %d, want 1", len(got.TXT))
	}
	want := map[string]string{"path": "/", "version": "2"}
	if len(got.TXT[0].Text) != len(want) {
		t.Fatalf("TXT[0].Text = %+v, want %+v", got.TXT[0].Text, want)
	}
	for k, v := range want {
		if got.TXT[0].Text[k] != v {
			t.Errorf("TXT[0].Text[%q] = %q, want %q", k, got.TXT[0].Text[k], v)
		}
	}
}

func TestParseAnswers_A(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.A{Hdr: rrHeader("foo.local.", dns.TypeA, 120), A: net.ParseIP("192.168.1.5")},
	}

	got := parseAnswers(msg)

	if len(got.A) != 1 {
		t.Fatalf("A count = %d, want 1", len(got.A))
	}
	if got.A[0].Name != "foo.local." || !got.A[0].IP.Equal(net.ParseIP("192.168.1.5")) || got.A[0].TTL != 120*time.Second {
		t.Errorf("A[0] = %+v", got.A[0])
	}
}

func TestParseAnswers_AAAA(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.AAAA{Hdr: rrHeader("foo.local.", dns.TypeAAAA, 120), AAAA: net.ParseIP("fe80::1")},
	}

	got := parseAnswers(msg)

	if len(got.AAAA) != 1 {
		t.Fatalf("AAAA count = %d, want 1", len(got.AAAA))
	}
	if got.AAAA[0].Name != "foo.local." || !got.AAAA[0].IP.Equal(net.ParseIP("fe80::1")) || got.AAAA[0].TTL != 120*time.Second {
		t.Errorf("AAAA[0] = %+v", got.AAAA[0])
	}
}

// mDNS responses commonly split PTR/SRV/TXT/A across Answer + Ns + Extra
// sections rather than putting everything in Answer; scan all three (mirrors
// how dnssd's own cache.go filterRecords does it).
func TestParseAnswers_ScansAllSections(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{&dns.PTR{Hdr: rrHeader("_http._tcp.local.", dns.TypePTR, 120), Ptr: "Foo._http._tcp.local."}}
	msg.Ns = []dns.RR{&dns.SRV{Hdr: rrHeader("Foo._http._tcp.local.", dns.TypeSRV, 120), Target: "foo.local.", Port: 80}}
	msg.Extra = []dns.RR{&dns.A{Hdr: rrHeader("foo.local.", dns.TypeA, 120), A: net.ParseIP("10.0.0.1")}}

	got := parseAnswers(msg)

	if len(got.PTR) != 1 || len(got.SRV) != 1 || len(got.A) != 1 {
		t.Fatalf("got PTR=%d SRV=%d A=%d, want 1 each", len(got.PTR), len(got.SRV), len(got.A))
	}
}

func TestParseAnswers_IgnoresUnknownTypes(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{
		&dns.NSEC{Hdr: rrHeader("foo.local.", dns.TypeNSEC, 120), NextDomain: "zzz.local."},
	}

	got := parseAnswers(msg)

	if len(got.PTR)+len(got.SRV)+len(got.TXT)+len(got.A)+len(got.AAAA) != 0 {
		t.Fatalf("expected nothing parsed from an unknown type, got %+v", got)
	}
}
