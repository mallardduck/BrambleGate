package vlancache

import "sync"

// live tracks the VlanCache instances belonging to the currently running
// engine, so admin actions (the GUI's "Clear DNS cache") can reach them
// without a handle on the CoreDNS plugin chain. setup registers each
// instance and unregisters it when its Corefile instance shuts down or is
// replaced by a reload.
var (
	liveMu sync.Mutex
	live   = map[*VlanCache]struct{}{}
)

func register(vc *VlanCache) {
	liveMu.Lock()
	live[vc] = struct{}{}
	liveMu.Unlock()
}

func unregister(vc *VlanCache) {
	liveMu.Lock()
	delete(live, vc)
	liveMu.Unlock()
}

// Flush empties the cache of every running vlancache instance and returns
// the total number of entries dropped.
func Flush() int {
	liveMu.Lock()
	defer liveMu.Unlock()
	n := 0
	for vc := range live {
		n += vc.store.clear()
	}
	return n
}
