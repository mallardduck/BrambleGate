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
