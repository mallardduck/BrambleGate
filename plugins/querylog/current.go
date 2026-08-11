package querylog

import "sync"

// current holds the single process-wide Ring wired into the running CoreDNS
// chain — the same "process-wide singleton read by other packages at request
// time" shape as vlanmatch.Current(). setup() calls SetCurrent each time the
// querylog directive is (re-)parsed, so a reload always leaves Current()
// pointing at the ring actually in use. The GUI's live Query Log page
// (dev-docs/query-log.md) reads this directly, never the durable store.
var (
	mu      sync.RWMutex
	current *Ring
)

// SetCurrent replaces the process-wide shared Ring.
func SetCurrent(r *Ring) {
	mu.Lock()
	defer mu.Unlock()
	current = r
}

// Current returns the process-wide shared Ring, or nil if querylog has never
// been set up (not present in the rendered Corefile, or no reload yet).
func Current() *Ring {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
