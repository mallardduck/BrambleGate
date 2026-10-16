package vlancache

import (
	"strings"
	"sync"

	"github.com/miekg/dns"
)

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

// FlushName drops every cached entry for exactly name across all running
// instances, for every record type. Subdomains are separate names and are
// not affected. Returns the number of entries dropped.
func FlushName(name string) int {
	name = dns.CanonicalName(strings.TrimSpace(name))
	liveMu.Lock()
	defer liveMu.Unlock()
	n := 0
	for vc := range live {
		n += vc.store.clearName(name)
	}
	return n
}
