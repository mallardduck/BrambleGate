package vlanmatch

import "sync"

// current holds the single process-wide Table representing the app's actual
// configured VLANs (settings.yaml's VLANs list) — see the package doc for
// why this is a distinct concept from Table itself.
//
// The composition root (internal/cli) and the GUI service
// (internal/gui/service.go's render) call SetCurrent whenever settings might
// have changed. localrecords/mdnsbridge call Current() as their real source
// of truth at request time, instead of each maintaining their own
// independently-refreshed copy — which is what previously let them drift
// out of sync with each other and with settings.yaml.
var (
	mu      sync.RWMutex
	current Table
)

// SetCurrent replaces the process-wide configured-VLANs table.
func SetCurrent(t Table) {
	mu.Lock()
	defer mu.Unlock()
	current = t
}

// Current returns the process-wide configured-VLANs table. Safe to call
// before SetCurrent is ever invoked — the zero Table matches nothing, same
// as "no VLANs declared".
func Current() Table {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
