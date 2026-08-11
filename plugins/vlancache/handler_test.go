package vlancache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// clientWriter lets a test choose the client source IP, mirroring
// plugins/localrecords' test helper of the same name.
type clientWriter struct {
	*test.ResponseWriter
	ip string
}

func (c *clientWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(c.ip), Port: 40000}
}

// stubNext counts calls and returns whatever build/inject wants, so tests can
// assert on cache hits (no call) vs misses (a call) directly.
type stubNext struct {
	calls int
	fn    func(r *dns.Msg) *dns.Msg
}

func (s *stubNext) Name() string { return "stub" }

func (s *stubNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	s.calls++
	m := s.fn(r)
	_ = w.WriteMsg(m)
	return m.Rcode, nil
}

func aRecord(name, ttl string) dns.RR {
	rr, err := dns.NewRR(name + " " + ttl + " IN A 192.0.2.1")
	if err != nil {
		panic(err)
	}
	return rr
}

func build(t *testing.T, next *stubNext) *VlanCache {
	t.Helper()
	t.Cleanup(func() { vlanmatch.SetCurrent(vlanmatch.Table{}) })
	vlanmatch.SetCurrent(vlanmatch.NewTable([]vlanmatch.VLAN{
		{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
		{Name: "guests", CIDRs: []string{"192.168.40.0/24"}},
	}))
	vc := &VlanCache{
		vlans:   vlanmatch.Current(),
		store:   newStore(defaultCap),
		failTTL: defaultFailTTL,
		Next:    next,
	}
	return vc
}

func queryFrom(vc *VlanCache, clientIP, name string, qtype uint16) *dns.Msg {
	r := new(dns.Msg)
	r.SetQuestion(dns.Fqdn(name), qtype)
	rec := dnstest.NewRecorder(&clientWriter{ResponseWriter: &test.ResponseWriter{}, ip: clientIP})
	vc.ServeDNS(context.Background(), rec, r)
	return rec.Msg
}

func TestCacheHitAvoidsSecondUpstreamCall(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "300")}
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	queryFrom(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA) // same VLAN, different host
	if next.calls != 1 {
		t.Fatalf("want 1 upstream call (second query should hit cache), got %d", next.calls)
	}
}

func TestDifferentVLANsGetIndependentCacheEntries(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "300")}
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA) // trusted
	queryFrom(vc, "192.168.40.5", "nas.home.arpa", dns.TypeA) // guests
	if next.calls != 2 {
		t.Fatalf("want 2 upstream calls (distinct VLAN buckets must not share an entry), got %d", next.calls)
	}
	// Repeat queries against each VLAN should now be served from that
	// VLAN's own cached entry.
	queryFrom(vc, "192.168.10.7", "nas.home.arpa", dns.TypeA)
	queryFrom(vc, "192.168.40.7", "nas.home.arpa", dns.TypeA)
	if next.calls != 2 {
		t.Fatalf("want still 2 upstream calls, got %d", next.calls)
	}
}

func TestServfailIsCachedAndExpires(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		return m
	}}
	vc := build(t, next)
	vc.failTTL = 2 * time.Second

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	m := queryFrom(vc, "192.168.10.5", "pihole.lan", dns.TypeAAAA)
	if m.Rcode != dns.RcodeServerFailure {
		t.Fatalf("want SERVFAIL, got %s", dns.RcodeToString[m.Rcode])
	}
	queryFrom(vc, "192.168.10.6", "pihole.lan", dns.TypeAAAA) // same VLAN, within failTTL
	if next.calls != 1 {
		t.Fatalf("want 1 upstream call (SERVFAIL storm should be deduped), got %d", next.calls)
	}

	// Advance past failTTL: the entry should no longer be served.
	now = now.Add(3 * time.Second)
	queryFrom(vc, "192.168.10.7", "pihole.lan", dns.TypeAAAA)
	if next.calls != 2 {
		t.Fatalf("want 2 upstream calls after failTTL expiry, got %d", next.calls)
	}
}

func TestScopeEchoOverridesVLANDefault(t *testing.T) {
	// Upstream echoes a /24 scope covering both 192.168.10.5 and
	// 192.168.10.6 (same VLAN here, but the point is the scope-derived
	// entry is what's consulted, not the VLAN bucket) yet excludes
	// 192.168.40.5 in the other VLAN.
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("ads.example.", "300")}
		m.SetEdns0(4096, false)
		opt := m.IsEdns0()
		opt.Option = append(opt.Option, &dns.EDNS0_SUBNET{
			Code:          dns.EDNS0SUBNET,
			Family:        1,
			SourceNetmask: 32,
			SourceScope:   24,
			Address:       net.ParseIP("192.168.10.5"),
		})
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "ads.example", dns.TypeA)
	if next.calls != 1 {
		t.Fatalf("want 1 upstream call, got %d", next.calls)
	}
	queryFrom(vc, "192.168.10.6", "ads.example", dns.TypeA) // within the echoed /24
	if next.calls != 1 {
		t.Fatalf("want still 1 upstream call (covered by echoed /24 scope), got %d", next.calls)
	}
	queryFrom(vc, "192.168.40.5", "ads.example", dns.TypeA) // outside the echoed /24
	if next.calls != 2 {
		t.Fatalf("want 2 upstream calls (client outside echoed scope must not hit the /24 entry), got %d", next.calls)
	}
}

func TestNoScopeEchoFallsBackToVLANBucket(t *testing.T) {
	// No EDNS0_SUBNET in the response at all — the lab's Pi-hole today, and
	// any resolver that doesn't implement RFC 7871 response scope.
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("plain.example.", "300")}
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "plain.example", dns.TypeA)
	queryFrom(vc, "192.168.10.6", "plain.example", dns.TypeA)
	if next.calls != 1 {
		t.Fatalf("want 1 upstream call, got %d", next.calls)
	}
	queryFrom(vc, "192.168.40.5", "plain.example", dns.TypeA) // different VLAN bucket
	if next.calls != 2 {
		t.Fatalf("want 2 upstream calls, got %d", next.calls)
	}
}

func TestNXDomainWithoutSOAIsNotCached(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeNameError
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "missing.example", dns.TypeA)
	queryFrom(vc, "192.168.10.6", "missing.example", dns.TypeA)
	if next.calls != 2 {
		t.Fatalf("NXDOMAIN without SOA must never be cached (no denial TTL available), got %d calls", next.calls)
	}
}

var _ plugin.Handler = (*stubNext)(nil)
