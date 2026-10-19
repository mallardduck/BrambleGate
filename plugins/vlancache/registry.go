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

// snapshot returns the currently registered instances. Callers walk the
// result without liveMu held, so a slow clear never stalls register or
// unregister (e.g. an engine reload). An instance that unregisters mid-walk
// is merely cleared needlessly; one that registers mid-walk starts empty.
func snapshot() []*VlanCache {
	liveMu.Lock()
	defer liveMu.Unlock()
	vcs := make([]*VlanCache, 0, len(live))
	for vc := range live {
		vcs = append(vcs, vc)
	}
	return vcs
}

// Flush empties the cache of every running vlancache instance and returns
// the total number of entries dropped.
func Flush() int {
	n := 0
	for _, vc := range snapshot() {
		n += vc.store.clear()
	}
	return n
}

// FlushName drops every cached entry that depends on name across all running
// instances, for every record type: entries for name itself plus aliases whose
// cached CNAME/DNAME chain passes through it. Subdomains are separate names
// and are not affected. Returns the number of entries dropped.
func FlushName(name string) int {
	name = dns.CanonicalName(strings.TrimSpace(name))
	n := 0
	for _, vc := range snapshot() {
		n += vc.store.clearName(name)
	}
	return n
}
