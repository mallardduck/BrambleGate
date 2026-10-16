package vlancache

import (
	"net"
	"testing"
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
