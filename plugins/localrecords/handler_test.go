package localrecords

import (
	"context"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

// build constructs a plugin instance with a fallthrough sentinel as Next.
func build(t *testing.T, corefile string) *LocalRecords {
	t.Helper()
	lr, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Next records whether fallthrough happened.
	lr.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused // sentinel: "fell through"
		_ = w.WriteMsg(m)
		return dns.RcodeRefused, nil
	})
	return lr
}

func query(lr *LocalRecords, name string, qtype uint16) *dns.Msg {
	r := new(dns.Msg)
	r.SetQuestion(dns.Fqdn(name), qtype)
	rec := dnstest.NewRecorder(&test.ResponseWriter{})
	lr.ServeDNS(context.Background(), rec, r)
	return rec.Msg
}

const corefile = `localrecords home.arpa {
    ttl 120
    record nas.home.arpa    A     192.168.10.20
    record git.home.arpa    CNAME nas.home.arpa
    record v6.home.arpa     AAAA  fd00::1
}`

func TestAnswersARecord(t *testing.T) {
	lr := build(t, corefile)
	m := query(lr, "nas.home.arpa", dns.TypeA)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 1 {
		t.Fatalf("want 1 answer NOERROR, got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	a, ok := m.Answer[0].(*dns.A)
	if !ok || a.A.String() != "192.168.10.20" {
		t.Fatalf("unexpected answer: %v", m.Answer[0])
	}
	if !m.Authoritative {
		t.Fatal("reply should be authoritative")
	}
	if a.Hdr.Ttl != 120 {
		t.Fatalf("ttl = %d, want 120", a.Hdr.Ttl)
	}
}

func TestCNAMEResolvesInZoneTarget(t *testing.T) {
	lr := build(t, corefile)
	m := query(lr, "git.home.arpa", dns.TypeA)
	if len(m.Answer) != 2 {
		t.Fatalf("want CNAME + resolved A, got %d: %v", len(m.Answer), m.Answer)
	}
	if _, ok := m.Answer[0].(*dns.CNAME); !ok {
		t.Fatalf("first answer should be CNAME, got %v", m.Answer[0])
	}
	if a, ok := m.Answer[1].(*dns.A); !ok || a.A.String() != "192.168.10.20" {
		t.Fatalf("second answer should resolve to nas A, got %v", m.Answer[1])
	}
}

func TestUnknownNameInZoneIsNXDOMAIN(t *testing.T) {
	lr := build(t, corefile)
	m := query(lr, "nope.home.arpa", dns.TypeA)
	if m.Rcode != dns.RcodeNameError {
		t.Fatalf("want NXDOMAIN, got rcode=%d", m.Rcode)
	}
	if len(m.Ns) != 1 {
		t.Fatalf("NXDOMAIN should carry an SOA in authority, got %d", len(m.Ns))
	}
}

func TestKnownNameWrongTypeIsNODATA(t *testing.T) {
	lr := build(t, corefile)
	m := query(lr, "nas.home.arpa", dns.TypeAAAA)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("want NODATA (NOERROR, no answers), got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	if len(m.Ns) != 1 {
		t.Fatalf("NODATA should carry an SOA, got %d", len(m.Ns))
	}
}

func TestOutOfZoneFallsThrough(t *testing.T) {
	lr := build(t, corefile)
	m := query(lr, "example.com", dns.TypeA)
	if m.Rcode != dns.RcodeRefused {
		t.Fatalf("out-of-zone query should fall through to Next (sentinel REFUSED), got rcode=%d", m.Rcode)
	}
}

func TestRecordOutsideZoneRejected(t *testing.T) {
	_, err := parse(caddy.NewTestController("dns", `localrecords home.arpa {
    record nas.example.com A 1.2.3.4
}`))
	if err == nil {
		t.Fatal("expected error for record outside owned zone")
	}
}
