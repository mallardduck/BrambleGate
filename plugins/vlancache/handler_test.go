package vlancache

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/plugins/querylog"
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

func build(t *testing.T, next plugin.Handler) *VlanCache {
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

// queryFromWithEntry mirrors queryFrom but wraps ctx with a querylog.Entry
// first, the way the real querylog plugin does ahead of vlancache in the
// chain — so tests can assert what vlancache self-attributes. Without this,
// querylog falls back to a latency heuristic (classifyFallback) that can't
// tell a coalesced singleflight follower (no new upstream call, but still
// slow — it waited on the leader) from a real forward, which is exactly what
// made a real-world SERVFAIL herd look unfixed in the query log.
func queryFromWithEntry(vc *VlanCache, clientIP, name string, qtype uint16) (*dns.Msg, *querylog.Entry) {
	r := new(dns.Msg)
	r.SetQuestion(dns.Fqdn(name), qtype)
	rec := dnstest.NewRecorder(&clientWriter{ResponseWriter: &test.ResponseWriter{}, ip: clientIP})
	e := &querylog.Entry{}
	ctx := querylog.NewContext(context.Background(), e)
	vc.ServeDNS(ctx, rec, r)
	return rec.Msg, e
}

func TestAttributesDirectCacheHit(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "300")}
		return m
	}}
	vc := build(t, next)

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA) // populate the cache
	_, e := queryFromWithEntry(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if e.Source != "vlancache" || e.Verdict != "cached" {
		t.Fatalf("Source/Verdict = %q/%q, want vlancache/cached", e.Source, e.Verdict)
	}
}

func TestAttributesLeaderFetchAsForward(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "300")}
		return m
	}}
	vc := build(t, next)

	_, e := queryFromWithEntry(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	if e.Source != "vlancache" || e.Verdict != "forwarded" {
		t.Fatalf("Source/Verdict = %q/%q, want vlancache/forwarded", e.Source, e.Verdict)
	}
}

func TestAttributesCoalescedFollowerDistinctFromForward(t *testing.T) {
	next := &blockingNext{}
	vc := build(t, next)

	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	entries := make([]*querylog.Entry, 2)
	wg.Add(2)
	ready.Add(2)
	for i := range 2 {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			_, entries[i] = queryFromWithEntry(vc, "192.168.10.5", "pihole.lan", dns.TypeAAAA)
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()

	var forwards, coalesced int
	for _, e := range entries {
		if e.Source != "vlancache" {
			t.Fatalf("Source = %q, want vlancache", e.Source)
		}
		switch e.Verdict {
		case "forwarded":
			forwards++
		case "coalesced":
			coalesced++
		default:
			t.Fatalf("Verdict = %q, want forwarded or coalesced", e.Verdict)
		}
	}
	if forwards != 1 || coalesced != 1 {
		t.Fatalf("want exactly 1 forward + 1 coalesced (the real upstream call vs. the deduped one), got %d forward + %d coalesced", forwards, coalesced)
	}
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

// blockingNext holds every upstream call open for a fixed window, long
// enough for a concurrent herd of identical queries to overlap with it —
// reproducing the real-world scenario where Pi-hole is slow/timing out on a
// query and a burst of clients all ask for it before any answer is cached.
type blockingNext struct {
	mu    sync.Mutex
	calls int
}

func (b *blockingNext) Name() string { return "blocking" }

func (b *blockingNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	m := new(dns.Msg)
	m.SetReply(r)
	m.Rcode = dns.RcodeServerFailure
	_ = w.WriteMsg(m)
	return m.Rcode, nil
}

// TestConcurrentHerdCoalescesUpstreamCalls guards against the cache-stampede
// gap: without request coalescing, a burst of clients asking the same
// question while the first answer is still in flight all miss the (still
// empty) cache and each fire their own upstream call — the exact "SERVFAIL
// herd" observed in the lab despite vlancache's SERVFAIL caching.
func TestConcurrentHerdCoalescesUpstreamCalls(t *testing.T) {
	next := &blockingNext{}
	vc := build(t, next)

	const herd = 20
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	start := make(chan struct{})
	wg.Add(herd)
	ready.Add(herd)
	for range herd {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			queryFrom(vc, "192.168.10.5", "pihole.lan", dns.TypeAAAA)
		}()
	}
	ready.Wait() // all goroutines are dispatched before any of them queries
	close(start) // release them at once so they race into ServeDNS together
	wg.Wait()

	next.mu.Lock()
	calls := next.calls
	next.mu.Unlock()
	if calls != 1 {
		t.Fatalf("want 1 upstream call for a concurrent herd of identical queries, got %d", calls)
	}
}

// bucketNext answers with a payload that encodes which VLAN subnet the
// upstream believes it's serving, derived from the leader's RemoteAddr (the
// only client info a coalesced call sees). This lets a test tell whether a
// coalesced answer that should have stayed VLAN-scoped leaked across buckets.
type bucketNext struct {
	mu    sync.Mutex
	calls int
}

func (b *bucketNext) Name() string { return "bucket" }

func (b *bucketNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	answerIP := "192.0.2.10" // trusted
	if strings.HasPrefix(w.RemoteAddr().String(), "192.168.40.") {
		answerIP = "192.0.2.40" // guests
	}
	rr, err := dns.NewRR("nas.home.arpa. 300 IN A " + answerIP)
	if err != nil {
		panic(err)
	}
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{rr}
	_ = w.WriteMsg(m)
	return m.Rcode, nil
}

// TestConcurrentHerdRespectsVLANSplitHorizon guards against request
// coalescing (added for TestConcurrentHerdCoalescesUpstreamCalls) widening
// its sharing beyond the direct tier's own bucket boundary: a herd spanning
// two VLANs must still produce one upstream call per VLAN, and each client
// must get only its own VLAN's answer, never the other bucket's.
func TestConcurrentHerdRespectsVLANSplitHorizon(t *testing.T) {
	next := &bucketNext{}
	vc := build(t, next)

	const perVLAN = 10
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	wg.Add(perVLAN * 2)
	ready.Add(perVLAN * 2)

	trusted := make([]*dns.Msg, perVLAN)
	guests := make([]*dns.Msg, perVLAN)
	for i := range perVLAN {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			trusted[i] = queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
		}()
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			guests[i] = queryFrom(vc, "192.168.40.5", "nas.home.arpa", dns.TypeA)
		}()
	}
	ready.Wait() // all goroutines dispatched before any of them queries
	close(start) // release them at once so both VLANs' herds race together
	wg.Wait()

	next.mu.Lock()
	calls := next.calls
	next.mu.Unlock()
	if calls != 2 {
		t.Fatalf("want 2 upstream calls (one per VLAN bucket — coalescing must not cross buckets), got %d", calls)
	}

	for i, m := range trusted {
		got := m.Answer[0].(*dns.A).A.String()
		if got != "192.0.2.10" {
			t.Fatalf("trusted client %d got %s, want the trusted VLAN's own answer (192.0.2.10), not another bucket's", i, got)
		}
	}
	for i, m := range guests {
		got := m.Answer[0].(*dns.A).A.String()
		if got != "192.0.2.40" {
			t.Fatalf("guest client %d got %s, want the guests VLAN's own answer (192.0.2.40), not another bucket's", i, got)
		}
	}
}

// edns0SubnetOpt builds an OPT RR carrying an RFC 7871 EDNS0_SUBNET option
// with the given SourceScope, simulating an upstream that echoes a
// host-specific policy scope back to the resolver.
func edns0SubnetOpt(ip net.IP, scope uint8) dns.RR {
	o := new(dns.OPT)
	o.Hdr.Name = "."
	o.Hdr.Rrtype = dns.TypeOPT
	o.Option = append(o.Option, &dns.EDNS0_SUBNET{
		Code:          dns.EDNS0SUBNET,
		Family:        1,
		SourceNetmask: 32,
		SourceScope:   scope,
		Address:       ip,
	})
	return o
}

// perHostNext answers with a payload that differs per exact requester
// address (not just per VLAN bucket), and echoes an RFC 7871 SourceScope=32
// tied to that same address — simulating an upstream doing real per-host
// policy (e.g. Pi-hole group assignment), the case the direct tier's
// bucket-wide default explicitly defers to when a narrower scope is echoed.
type perHostNext struct {
	mu    sync.Mutex
	calls int
}

func (p *perHostNext) Name() string { return "perhost" }

func (p *perHostNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	ip := w.RemoteAddr().(*net.UDPAddr).IP
	answerIP := "192.0.2.100"
	if ip.Equal(net.ParseIP("192.168.10.6")) {
		answerIP = "192.0.2.200"
	}
	rr, err := dns.NewRR("host.trusted.arpa. 300 IN A " + answerIP)
	if err != nil {
		panic(err)
	}
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{rr}
	m.Extra = []dns.RR{edns0SubnetOpt(ip, 32)}
	_ = w.WriteMsg(m)
	return m.Rcode, nil
}

// TestConcurrentHerdNarrowScopeDoesNotLeakAcrossHosts guards the gap in the
// first version of request coalescing: bucket-level singleflight alone would
// hand every requester in a VLAN the *leader's* answer, even when the
// upstream's echoed scope says that answer is only valid for the leader's
// own address. Two hosts in the same trusted VLAN, each in their own
// concurrent herd, must still get their own scoped answer — and same-host
// duplicates must still coalesce to one upstream call each, not one per
// query.
func TestConcurrentHerdNarrowScopeDoesNotLeakAcrossHosts(t *testing.T) {
	next := &perHostNext{}
	vc := build(t, next)

	const burstPerHost = 5
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	wg.Add(burstPerHost * 2)
	ready.Add(burstPerHost * 2)

	hostA := make([]*dns.Msg, burstPerHost) // 192.168.10.5
	hostB := make([]*dns.Msg, burstPerHost) // 192.168.10.6 — same VLAN bucket, different host
	for i := range burstPerHost {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			hostA[i] = queryFrom(vc, "192.168.10.5", "host.trusted.arpa", dns.TypeA)
		}()
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			hostB[i] = queryFrom(vc, "192.168.10.6", "host.trusted.arpa", dns.TypeA)
		}()
	}
	ready.Wait() // all goroutines dispatched before any of them queries
	close(start) // release them at once so both hosts' bursts race together
	wg.Wait()

	next.mu.Lock()
	calls := next.calls
	next.mu.Unlock()
	// One call per distinct host, not one per bucket and not one per query:
	// a /32-scoped answer must not be shared across hosts, but same-host
	// duplicates within the burst still coalesce.
	if calls != 2 {
		t.Fatalf("want 2 upstream calls (one per distinct host address), got %d", calls)
	}

	for i, m := range hostA {
		got := m.Answer[0].(*dns.A).A.String()
		if got != "192.0.2.100" {
			t.Fatalf("host A query %d got %s, want its own scoped answer (192.0.2.100), not host B's", i, got)
		}
	}
	for i, m := range hostB {
		got := m.Answer[0].(*dns.A).A.String()
		if got != "192.0.2.200" {
			t.Fatalf("host B query %d got %s, want its own scoped answer (192.0.2.200), not host A's", i, got)
		}
	}
}

// timeoutNext simulates the real forward plugin's behavior on a genuine
// upstream connection failure (plugin/forward's ServeDNS: once every proxy's
// connect attempt has timed out/failed, it returns (dns.RcodeServerFailure,
// upstreamErr) without ever calling WriteMsg — the client's eventual SERVFAIL
// comes from CoreDNS's own top-level fallback, not from a real DNS response
// forward received). None of this package's other test doubles exercise
// that: they all call w.WriteMsg with a real *dns.Msg. That gap is exactly
// why the AAAA storm kept happening in production despite this plugin's
// SERVFAIL caching — a connection-level timeout never reached the
// cacheable() path at all, because fetch bailed out on the bare error
// before ever looking at a response.
type timeoutNext struct {
	mu    sync.Mutex
	calls int
}

func (t *timeoutNext) Name() string { return "timeout" }

func (t *timeoutNext) ServeDNS(_ context.Context, _ dns.ResponseWriter, _ *dns.Msg) (int, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return dns.RcodeServerFailure, errors.New("simulated: no healthy upstream")
}

func TestUpstreamConnectionFailureIsCachedAndExpires(t *testing.T) {
	next := &timeoutNext{}
	vc := build(t, next)
	vc.failTTL = 2 * time.Second

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	m := queryFrom(vc, "192.168.10.5", "pihole.lan", dns.TypeAAAA)
	if m == nil || m.Rcode != dns.RcodeServerFailure {
		t.Fatalf("want a written SERVFAIL reply, got %+v", m)
	}
	next.mu.Lock()
	calls := next.calls
	next.mu.Unlock()
	if calls != 1 {
		t.Fatalf("want 1 upstream call, got %d", calls)
	}

	queryFrom(vc, "192.168.10.6", "pihole.lan", dns.TypeAAAA) // same VLAN, within failTTL
	next.mu.Lock()
	calls = next.calls
	next.mu.Unlock()
	if calls != 1 {
		t.Fatalf("want 1 upstream call (connection-failure storm should be deduped), got %d", calls)
	}

	now = now.Add(3 * time.Second)
	queryFrom(vc, "192.168.10.7", "pihole.lan", dns.TypeAAAA)
	next.mu.Lock()
	calls = next.calls
	next.mu.Unlock()
	if calls != 2 {
		t.Fatalf("want 2 upstream calls after failTTL expiry, got %d", calls)
	}
}

func TestAttributesUpstreamConnectionFailureAsForward(t *testing.T) {
	next := &timeoutNext{}
	vc := build(t, next)

	_, e := queryFromWithEntry(vc, "192.168.10.5", "pihole.lan", dns.TypeAAAA)
	if e.Source != "vlancache" || e.Verdict != "forwarded" {
		t.Fatalf("Source/Verdict = %q/%q, want vlancache/forwarded", e.Source, e.Verdict)
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
