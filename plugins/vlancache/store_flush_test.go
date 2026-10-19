package vlancache

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestFlushClearsBothTiersOfLiveInstances(t *testing.T) {
	vc := &VlanCache{store: newStore(100)}
	register(vc)
	defer unregister(vc)

	vc.store.setDirect(1, &entry{})
	vc.store.setDirect(2, &entry{})
	_, p, _ := net.ParseCIDR("10.0.0.0/24")
	vc.store.setScoped(3, p, &entry{})

	if n := Flush(); n != 3 {
		t.Fatalf("Flush() = %d, want 3", n)
	}
	if _, ok := vc.store.direct.Get(1); ok {
		t.Fatal("direct entry survived flush")
	}
	if len(vc.store.scoped) != 0 {
		t.Fatal("scoped entries survived flush")
	}
}

func TestFlushNameMatchesExactNameOnly(t *testing.T) {
	vc := &VlanCache{store: newStore(100)}
	register(vc)
	defer unregister(vc)

	vc.store.setDirect(1, &entry{qname: "a.example.com."})
	vc.store.setDirect(2, &entry{qname: "a.example.com."}) // other qtype
	vc.store.setDirect(3, &entry{qname: "sub.a.example.com."})
	vc.store.setDirect(4, &entry{qname: "b.example.com."})
	_, p, _ := net.ParseCIDR("10.0.0.0/24")
	vc.store.setScoped(5, p, &entry{qname: "a.example.com."})

	if n := FlushName(" A.Example.com "); n != 3 {
		t.Fatalf("FlushName() = %d, want 3", n)
	}
	for _, k := range []uint64{3, 4} {
		if _, ok := vc.store.direct.Get(k); !ok {
			t.Fatalf("entry %d wrongly removed", k)
		}
	}
	if len(vc.store.scoped) != 0 {
		t.Fatal("scoped entry survived")
	}
}

// A flush walking a slow store must not block register/unregister: a reload
// swapping instances would otherwise stall behind the walk.
func TestFlushDoesNotBlockRegistryDuringWalk(t *testing.T) {
	slow := &VlanCache{store: newStore(100)}
	register(slow)
	defer unregister(slow)

	// Holding the scoped-tier lock makes slow.store.clear() park mid-walk.
	slow.store.mu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			slow.store.mu.Unlock()
		}
	}
	defer release()

	flushed := make(chan struct{})
	go func() { Flush(); close(flushed) }()
	time.Sleep(50 * time.Millisecond) // let Flush reach the blocked clear

	other := &VlanCache{store: newStore(100)}
	registered := make(chan struct{})
	go func() { register(other); close(registered) }()
	defer unregister(other)

	select {
	case <-registered:
	case <-time.After(time.Second):
		release() // let Flush finish so the deferred unregisters can't deadlock
		t.Fatal("register blocked behind an in-progress Flush walk")
	}

	release()
	<-flushed
}

// A fetch already in flight when a flush lands holds a pre-flush answer. The
// client that asked still gets it, but it must not be stored: the flush was
// meant to evict exactly that data.
func TestFlushDiscardsInFlightFetchResult(t *testing.T) {
	for name, flush := range map[string]func() int{
		"Flush":     Flush,
		"FlushName": func() int { return FlushName("slow.example") },
	} {
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			proceed := make(chan struct{})
			var once sync.Once
			next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
				once.Do(func() {
					close(started)
					<-proceed
				})
				m := new(dns.Msg)
				m.SetReply(r)
				m.Answer = []dns.RR{aRecord("slow.example.", "300")}
				return m
			}}
			vc := build(t, next)
			register(vc)
			defer unregister(vc)

			done := make(chan *dns.Msg)
			go func() { done <- queryFrom(vc, "192.168.10.5", "slow.example", dns.TypeA) }()
			<-started

			flush()
			close(proceed)
			if got := <-done; got == nil || len(got.Answer) != 1 {
				t.Fatalf("in-flight client must still receive its answer, got %v", got)
			}

			queryFrom(vc, "192.168.10.5", "slow.example", dns.TypeA)
			if next.calls != 2 {
				t.Fatalf("pre-flush answer was cached after the flush: want 2 upstream calls, got %d", next.calls)
			}
		})
	}
}

func cnameRR(owner, target string) dns.RR {
	return &dns.CNAME{Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeCNAME, Class: dns.ClassINET}, Target: target}
}

// FlushName must also drop entries whose cached CNAME chain passes through
// the flushed name: the alias entry is keyed only by its own question name,
// so an exact-qname match leaves it serving the old target until TTL.
func TestFlushNameClearsAliasesWhoseChainIncludesName(t *testing.T) {
	_, scope, _ := net.ParseCIDR("10.0.0.0/24")

	tests := []struct {
		name  string
		flush string
		want  int
		// entry key -> whether it should be gone after the flush
		gone map[uint64]bool
	}{
		{
			name:  "alias cleared when its CNAME target is flushed",
			flush: "cdn.example.net.",
			want:  2,
			gone:  map[uint64]bool{1: true, 2: false, 3: true},
		},
		{
			name:  "two-hop chain cleared by the middle name",
			flush: "b.example.net.",
			want:  1,
			gone:  map[uint64]bool{1: false, 2: true, 3: false},
		},
		{
			name:  "two-hop chain cleared by the last name",
			flush: "c.example.net.",
			want:  1,
			gone:  map[uint64]bool{1: false, 2: true, 3: false},
		},
		{
			name:  "NODATA chain end appears only as a CNAME target",
			flush: "dead.example.net.",
			want:  1,
			gone:  map[uint64]bool{1: false, 2: false, 3: false, 4: true},
		},
		{
			name:  "target match is case-insensitive",
			flush: "CDN.Example.NET.",
			want:  2,
			gone:  map[uint64]bool{1: true, 2: false, 3: true},
		},
		{
			name:  "subdomain of a chain name is not matched",
			flush: "sub.cdn.example.net.",
			want:  0,
			gone:  map[uint64]bool{1: false, 2: false, 3: false, 4: false},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vc := &VlanCache{store: newStore(100)}
			register(vc)
			defer unregister(vc)

			// 1: www -> cdn (direct tier)
			vc.store.setDirect(1, &entry{qname: "www.example.com.", answer: []dns.RR{
				cnameRR("www.example.com.", "cdn.example.net."),
				aRecord("cdn.example.net.", "60"),
			}})
			// 2: a -> b -> c (direct tier)
			vc.store.setDirect(2, &entry{qname: "a.example.com.", answer: []dns.RR{
				cnameRR("a.example.com.", "b.example.net."),
				cnameRR("b.example.net.", "c.example.net."),
				aRecord("c.example.net.", "60"),
			}})
			// 3: www2 -> cdn (scoped tier)
			vc.store.setScoped(3, scope, &entry{qname: "www2.example.com.", answer: []dns.RR{
				cnameRR("www2.example.com.", "cdn.example.net."),
				aRecord("cdn.example.net.", "60"),
			}})
			// 4: gone -> dead, chain ends in NODATA so dead.example.net. is
			// never an answer owner.
			vc.store.setDirect(4, &entry{qname: "gone.example.com.", answer: []dns.RR{
				cnameRR("gone.example.com.", "dead.example.net."),
			}})

			if n := FlushName(tc.flush); n != tc.want {
				t.Fatalf("FlushName(%q) = %d, want %d", tc.flush, n, tc.want)
			}
			for k, wantGone := range tc.gone {
				present := hasEntry(vc.store, k)
				if wantGone && present {
					t.Errorf("entry %d survived FlushName(%q)", k, tc.flush)
				}
				if !wantGone && !present {
					t.Errorf("entry %d wrongly removed by FlushName(%q)", k, tc.flush)
				}
			}
		})
	}
}

func hasEntry(s *store, key uint64) bool {
	if _, ok := s.direct.Get(key); ok {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.scoped[key]) > 0
}

// Black-box: after flushing the CNAME target, the next query for the alias
// must go back upstream instead of serving the cached chain.
func TestFlushNameRefetchesAliasThroughServeDNS(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{
			cnameRR("www.example.", "cdn.example."),
			aRecord("cdn.example.", "300"),
		}
		return m
	}}
	vc := build(t, next)
	register(vc)
	defer unregister(vc)

	queryFrom(vc, "192.168.10.5", "www.example", dns.TypeA)
	queryFrom(vc, "192.168.10.5", "www.example", dns.TypeA)
	if next.calls != 1 {
		t.Fatalf("second query should be a cache hit: want 1 upstream call, got %d", next.calls)
	}

	if n := FlushName("cdn.example"); n != 1 {
		t.Fatalf("FlushName(cdn.example) = %d, want 1 (the www alias)", n)
	}

	queryFrom(vc, "192.168.10.5", "www.example", dns.TypeA)
	if next.calls != 2 {
		t.Fatalf("alias still served from cache after its target was flushed: want 2 upstream calls, got %d", next.calls)
	}
}
