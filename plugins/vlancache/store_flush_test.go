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
