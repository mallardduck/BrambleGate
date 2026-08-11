package querylog

import "sync"

// current holds the single process-wide Ring wired into the running CoreDNS
// chain — the same "process-wide singleton read by other packages at request
// time" shape as vlanmatch.Current(). Shared by every listener/server block
// (see setup.go's ringMarker) and deliberately preserved across a CoreDNS
// reload — from the operator's point of view BrambleGate didn't restart, so
// query history shouldn't vanish just because CoreDNS's Instance did; setup()
// only calls SetCurrent with a fresh Ring when capacity genuinely changed or
// none exists yet. The GUI's live Query Log page (dev-docs/query-log.md)
// reads this directly, never the durable store.
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
