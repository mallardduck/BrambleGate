package querylog

import (
	"fmt"
	"sync"
	"testing"
)

func entryNamed(qname string) Entry {
	return Entry{QName: qname}
}

func TestRing_SnapshotEmpty(t *testing.T) {
	r := NewRing(3)
	got := r.Snapshot(Filter{})
	if len(got) != 0 {
		t.Errorf("Snapshot on empty ring = %v, want empty", got)
	}
}

func TestRing_PushBelowCapacity_NewestFirst(t *testing.T) {
	r := NewRing(5)
	r.Push(entryNamed("a"))
	r.Push(entryNamed("b"))
	r.Push(entryNamed("c"))

	got := r.Snapshot(Filter{})
	want := []string{"c", "b", "a"}
	assertQNames(t, got, want)
}

func TestRing_PushPastCapacity_EvictsOldest(t *testing.T) {
	r := NewRing(3)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		r.Push(entryNamed(n))
	}

	got := r.Snapshot(Filter{})
	// Capacity 3, 5 pushed ("a".."e") -> oldest two ("a","b") evicted, newest first.
	want := []string{"e", "d", "c"}
	assertQNames(t, got, want)
}

func TestRing_Snapshot_FilterQName(t *testing.T) {
	r := NewRing(10)
	r.Push(Entry{QName: "nas.home.arpa."})
	r.Push(Entry{QName: "printer.home.arpa."})
	r.Push(Entry{QName: "example.com."})

	got := r.Snapshot(Filter{QName: "home.arpa"})
	want := []string{"printer.home.arpa.", "nas.home.arpa."}
	assertQNames(t, got, want)
}

func TestRing_Snapshot_FilterQName_CaseInsensitive(t *testing.T) {
	r := NewRing(10)
	r.Push(Entry{QName: "NAS.home.arpa."})

	got := r.Snapshot(Filter{QName: "nas"})
	assertQNames(t, got, []string{"NAS.home.arpa."})
}

func TestRing_Snapshot_FilterClient(t *testing.T) {
	r := NewRing(10)
	r.Push(Entry{QName: "a", Client: ClientInfo{IP: "192.0.2.10"}})
	r.Push(Entry{QName: "b", Client: ClientInfo{IP: "192.0.2.20"}})

	got := r.Snapshot(Filter{Client: "192.0.2.1"})
	assertQNames(t, got, []string{"a"})
}

func TestRing_Snapshot_FilterVLAN_ExactMatch(t *testing.T) {
	r := NewRing(10)
	r.Push(Entry{QName: "a", Client: ClientInfo{VLAN: "trusted"}})
	r.Push(Entry{QName: "b", Client: ClientInfo{VLAN: "guest"}})
	r.Push(Entry{QName: "c", Client: ClientInfo{VLAN: ""}})

	got := r.Snapshot(Filter{VLAN: "trusted"})
	assertQNames(t, got, []string{"a"})
}

func TestRing_Snapshot_FiltersCombine(t *testing.T) {
	r := NewRing(10)
	r.Push(Entry{QName: "nas.home.arpa.", Client: ClientInfo{IP: "192.0.2.10", VLAN: "trusted"}})
	r.Push(Entry{QName: "nas.home.arpa.", Client: ClientInfo{IP: "192.0.2.20", VLAN: "guest"}})

	got := r.Snapshot(Filter{QName: "home.arpa", VLAN: "trusted"})
	if len(got) != 1 || got[0].Client.IP != "192.0.2.10" {
		t.Errorf("Snapshot combined filter = %+v, want single entry from 192.0.2.10", got)
	}
}

func TestRing_ConcurrentPushAndSnapshot(t *testing.T) {
	r := NewRing(64)
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Push(entryNamed(fmt.Sprintf("w%d-%d", i, j)))
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			_ = r.Snapshot(Filter{})
		}
	}()

	wg.Wait()

	got := r.Snapshot(Filter{})
	if len(got) != 64 {
		t.Errorf("Snapshot len after concurrent pushes = %d, want 64 (ring capacity)", len(got))
	}
}

func assertQNames(t *testing.T, got []Entry, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Snapshot len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, e := range got {
		if e.QName != want[i] {
			t.Errorf("Snapshot[%d].QName = %q, want %q", i, e.QName, want[i])
		}
	}
}
