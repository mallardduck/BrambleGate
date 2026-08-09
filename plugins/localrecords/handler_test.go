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

const fallthroughZoneJSON = `{
  "default_ttl": 300,
  "zones": ["home.arpa", "dns.example.com"],
  "vlans": [],
  "records": [
    {"name": "dns.example.com", "type": "A", "default": "192.168.10.53", "ttl": 300}
  ]
}`

func buildFallthrough(t *testing.T) *LocalRecords {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "records.json")
	if err := os.WriteFile(path, []byte(fallthroughZoneJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	corefile := "localrecords home.arpa dns.example.com {\n\tzonedata " + filepath.ToSlash(path) +
		"\n\tfallthrough dns.example.com\n}"
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

func TestFallthroughZoneAnswersDeclaredRecordLocally(t *testing.T) {
	lr := buildFallthrough(t)
	m := queryFrom(lr, "192.168.10.5", "dns.example.com", dns.TypeA)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 1 {
		t.Fatalf("want a local answer, got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	if a := m.Answer[0].(*dns.A); a.A.String() != "192.168.10.53" {
		t.Fatalf("want 192.168.10.53, got %s", a.A)
	}
}

func TestFallthroughZoneMissDefersToNext(t *testing.T) {
	lr := buildFallthrough(t)
	// No record for this name in the fallthrough zone — must fall through, not NXDOMAIN.
	m := queryFrom(lr, "192.168.10.5", "other.dns.example.com", dns.TypeA)
	if m.Rcode != dns.RcodeRefused {
		t.Fatalf("want fallthrough (sentinel REFUSED), got %d", m.Rcode)
	}
}

func TestFallthroughZoneWrongTypeDefersToNext(t *testing.T) {
	lr := buildFallthrough(t)
	// The name exists locally but not for AAAA — still defers (unlike a
	// non-fallthrough zone, which would answer NODATA).
	m := queryFrom(lr, "192.168.10.5", "dns.example.com", dns.TypeAAAA)
	if m.Rcode != dns.RcodeRefused {
		t.Fatalf("want fallthrough (sentinel REFUSED), got %d", m.Rcode)
	}
}

func TestNonFallthroughZoneUnaffected(t *testing.T) {
	lr := buildFallthrough(t)
	// home.arpa is still fully owned: an unknown name there is NXDOMAIN, not a
	// fallthrough, even though this LocalRecords also has a fallthrough zone.
	m := queryFrom(lr, "192.168.10.5", "nope.home.arpa", dns.TypeA)
	if m.Rcode != dns.RcodeNameError {
		t.Fatalf("want NXDOMAIN, got %d", m.Rcode)
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

const ddrZoneJSON = `{
  "default_ttl": 300,
  "zones": ["home.arpa", "resolver.arpa"],
  "vlans": [],
  "records": [],
  "ddr": [
    {"priority": 1, "target": "dns.example.com", "params": [
      {"key": "alpn", "value": "dot"}, {"key": "port", "value": "853"}
    ]},
    {"priority": 1, "target": "dns.example.com", "params": [
      {"key": "alpn", "value": "h2"}, {"key": "port", "value": "443"}, {"key": "dohpath", "value": "/dns-query{?dns}"}
    ]}
  ]
}`

func buildDDR(t *testing.T) *LocalRecords {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "records.json")
	if err := os.WriteFile(path, []byte(ddrZoneJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	corefile := "localrecords home.arpa resolver.arpa {\n\tzonedata " + filepath.ToSlash(path) + "\n}"
	lr, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return lr
}

func TestDDRAnswersSVCBForEachProtocol(t *testing.T) {
	lr := buildDDR(t)
	m := queryFrom(lr, "192.168.10.5", "_dns.resolver.arpa", dns.TypeSVCB)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 2 {
		t.Fatalf("want 2 SVCB answers, got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	byAlpn := map[string]*dns.SVCB{}
	for _, rr := range m.Answer {
		svcb, ok := rr.(*dns.SVCB)
		if !ok {
			t.Fatalf("expected *dns.SVCB, got %T", rr)
		}
		if svcb.Target != "dns.example.com." {
			t.Fatalf("unexpected target %q", svcb.Target)
		}
		for _, v := range svcb.Value {
			if alpn, ok := v.(*dns.SVCBAlpn); ok {
				byAlpn[alpn.Alpn[0]] = svcb
			}
		}
	}
	dot, ok := byAlpn["dot"]
	if !ok {
		t.Fatalf("missing dot SVCB record: %+v", m.Answer)
	}
	if !svcbHasPort(dot, 853) {
		t.Errorf("dot record missing port 853: %+v", dot.Value)
	}
	h2, ok := byAlpn["h2"]
	if !ok {
		t.Fatalf("missing h2 SVCB record: %+v", m.Answer)
	}
	if !svcbHasPort(h2, 443) || !svcbHasDoHPath(h2, "/dns-query{?dns}") {
		t.Errorf("h2 record missing port/dohpath: %+v", h2.Value)
	}
}

func svcbHasPort(svcb *dns.SVCB, port uint16) bool {
	for _, v := range svcb.Value {
		if p, ok := v.(*dns.SVCBPort); ok && p.Port == port {
			return true
		}
	}
	return false
}

func svcbHasDoHPath(svcb *dns.SVCB, template string) bool {
	for _, v := range svcb.Value {
		if p, ok := v.(*dns.SVCBDoHPath); ok && p.Template == template {
			return true
		}
	}
	return false
}

const ddrZoneWithAddressJSON = `{
  "default_ttl": 300,
  "zones": ["home.arpa", "dns.example.com", "resolver.arpa"],
  "vlans": [],
  "records": [
    {"name": "dns.example.com", "type": "A", "default": "192.168.10.53", "ttl": 0, "vlan_overrides": []}
  ],
  "ddr": [
    {"priority": 1, "target": "dns.example.com", "params": [
      {"key": "alpn", "value": "dot"}, {"key": "port", "value": "853"}
    ]}
  ]
}`

// TestDDRAnswerCarriesAddressGlue guards the DDR SVCB response's Additional
// section: without the target's own address there, an opportunistic client
// has to issue a second query to resolve it (RFC 9462 §4 SHOULD avoid this).
func TestDDRAnswerCarriesAddressGlue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.json")
	if err := os.WriteFile(path, []byte(ddrZoneWithAddressJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	corefile := "localrecords home.arpa dns.example.com resolver.arpa {\n\tzonedata " + filepath.ToSlash(path) + "\n}"
	lr, err := parse(caddy.NewTestController("dns", corefile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := queryFrom(lr, "192.168.10.5", "_dns.resolver.arpa", dns.TypeSVCB)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 1 {
		t.Fatalf("want 1 SVCB answer, got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	if len(m.Extra) != 1 {
		t.Fatalf("want 1 glue record in Additional, got %+v", m.Extra)
	}
	a, ok := m.Extra[0].(*dns.A)
	if !ok || a.Hdr.Name != "dns.example.com." || a.A.String() != "192.168.10.53" {
		t.Fatalf("unexpected glue record: %+v", m.Extra[0])
	}
}

func TestDDRWrongTypeIsNODATA(t *testing.T) {
	lr := buildDDR(t)
	m := queryFrom(lr, "192.168.10.5", "_dns.resolver.arpa", dns.TypeA)
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("want NODATA, got rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
}

func TestDDRUnrelatedNameInZoneIsNXDomain(t *testing.T) {
	lr := buildDDR(t)
	// resolver.arpa is NXDOMAIN-owned (not a fallthrough zone) — a special-use
	// domain nobody delegates, so a miss here must not fall through.
	m := queryFrom(lr, "192.168.10.5", "nope.resolver.arpa", dns.TypeA)
	if m.Rcode != dns.RcodeNameError {
		t.Fatalf("want NXDOMAIN, got %d", m.Rcode)
	}
}
