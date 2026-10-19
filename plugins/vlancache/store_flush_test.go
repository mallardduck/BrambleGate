package vlancache

import (
	"net"
	"testing"
	"time"
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
