package localrecords

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

const zoneJSON = `{
  "default_ttl": 300,
  "zones": ["home.arpa"],
  "vlans": [
    {"name": "trusted",   "cidrs": ["192.168.10.0/24"]},
    {"name": "untrusted", "cidrs": ["192.168.30.0/24"]},
    {"name": "guests",    "cidrs": ["192.168.40.0/24"]}
  ],
  "records": [
    {"name": "nas.home.arpa", "type": "A", "default": "192.168.10.20", "ttl": 300,
     "vlan_overrides": [
       {"vlan": "untrusted", "nxdomain": true},
       {"vlan": "guests", "ttl": 30},
       {"vlan": "trusted", "value": "10.10.10.10"}
     ]},
    {"name": "git.home.arpa", "type": "CNAME", "default": "nas.home.arpa"}
  ]
}`

func build(t *testing.T) *LocalRecords {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "records.json")
	if err := os.WriteFile(path, []byte(zoneJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	corefile := "localrecords home.arpa {\n\tzonedata " + filepath.ToSlash(path) + "\n}"
	lr, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lr.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused // sentinel: fell through
		_ = w.WriteMsg(m)
		return dns.RcodeRefused, nil
	})
	return lr
}

// clientWriter lets a test choose the client source IP for split-horizon.
type clientWriter struct {
	*test.ResponseWriter
	ip string
}

func (c *clientWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(c.ip), Port: 40000}
}

func queryFrom(lr *LocalRecords, clientIP, name string, qtype uint16) *dns.Msg {
	r := new(dns.Msg)
	r.SetQuestion(dns.Fqdn(name), qtype)
	rec := dnstest.NewRecorder(&clientWriter{ResponseWriter: &test.ResponseWriter{}, ip: clientIP})
	lr.ServeDNS(context.Background(), rec, r)
	return rec.Msg
}

func TestSplitHorizonValueOverride(t *testing.T) {
	lr := build(t)
	m := queryFrom(lr, "192.168.10.5", "nas.home.arpa", dns.TypeA) // trusted → overridden value
	if len(m.Answer) != 1 {
		t.Fatalf("want 1 answer, got %d", len(m.Answer))
	}
	if a := m.Answer[0].(*dns.A); a.A.String() != "10.10.10.10" {
		t.Fatalf("trusted VLAN should get overridden 10.10.10.10, got %s", a.A)
	}
}

func TestSplitHorizonNXDomainForVLAN(t *testing.T) {
	lr := build(t)
	m := queryFrom(lr, "192.168.30.5", "nas.home.arpa", dns.TypeA) // untrusted → nxdomain
	if m.Rcode != dns.RcodeNameError {
		t.Fatalf("untrusted VLAN should get NXDOMAIN, got rcode=%d", m.Rcode)
	}
}

func TestSplitHorizonTTLOnlyOverrideInheritsValue(t *testing.T) {
	lr := build(t)
	m := queryFrom(lr, "192.168.40.5", "nas.home.arpa", dns.TypeA) // guests → default value, ttl 30
	if len(m.Answer) != 1 {
		t.Fatalf("want 1 answer, got %d", len(m.Answer))
	}
	a := m.Answer[0].(*dns.A)
	if a.A.String() != "192.168.10.20" || a.Hdr.Ttl != 30 {
		t.Fatalf("guests should get default value with ttl 30, got %s ttl=%d", a.A, a.Hdr.Ttl)
	}
}

func TestUnmatchedVLANGetsDefault(t *testing.T) {
	lr := build(t)
	m := queryFrom(lr, "10.0.0.1", "nas.home.arpa", dns.TypeA) // no VLAN match → default
	if len(m.Answer) != 1 {
		t.Fatalf("want 1 answer, got %d", len(m.Answer))
	}
	a := m.Answer[0].(*dns.A)
	if a.A.String() != "192.168.10.20" || a.Hdr.Ttl != 300 {
		t.Fatalf("unmatched client should get default 192.168.10.20 ttl 300, got %s ttl=%d", a.A, a.Hdr.Ttl)
	}
}

func TestCNAMEChasedRespectsHorizon(t *testing.T) {
	lr := build(t)
	// From trusted, git → nas CNAME, chased to the trusted-overridden A.
	m := queryFrom(lr, "192.168.10.5", "git.home.arpa", dns.TypeA)
	if len(m.Answer) != 2 {
		t.Fatalf("want CNAME + A, got %d: %v", len(m.Answer), m.Answer)
	}
	if _, ok := m.Answer[0].(*dns.CNAME); !ok {
		t.Fatalf("first answer should be CNAME, got %v", m.Answer[0])
	}
	if a := m.Answer[1].(*dns.A); a.A.String() != "10.10.10.10" {
		t.Fatalf("chased A should honor the trusted override, got %s", a.A)
	}

	// From untrusted, git aliases nas but nas is NXDOMAIN there: CNAME returned,
	// no A appended.
	m2 := queryFrom(lr, "192.168.30.5", "git.home.arpa", dns.TypeA)
	if len(m2.Answer) != 1 {
		t.Fatalf("want just the CNAME (target suppressed), got %d: %v", len(m2.Answer), m2.Answer)
	}
	if _, ok := m2.Answer[0].(*dns.CNAME); !ok {
		t.Fatalf("expected a CNAME answer, got %v", m2.Answer[0])
	}
}

func TestUnknownNameNXDomainAndOutOfZoneFallsThrough(t *testing.T) {
	lr := build(t)
	if m := queryFrom(lr, "192.168.10.5", "nope.home.arpa", dns.TypeA); m.Rcode != dns.RcodeNameError {
		t.Fatalf("unknown in-zone name should be NXDOMAIN, got %d", m.Rcode)
	}
	if m := queryFrom(lr, "192.168.10.5", "example.com", dns.TypeA); m.Rcode != dns.RcodeRefused {
		t.Fatalf("out-of-zone should fall through (sentinel REFUSED), got %d", m.Rcode)
	}
}

func TestNODATAForWrongType(t *testing.T) {
	lr := build(t)
	m := queryFrom(lr, "192.168.10.5", "nas.home.arpa", dns.TypeAAAA) // exists as A, not AAAA
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("want NODATA (NOERROR, no answers), got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	if len(m.Ns) != 1 {
		t.Fatalf("NODATA should carry an SOA, got %d", len(m.Ns))
	}
}
