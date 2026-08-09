// Package pluginreg is a tiny, dependency-free registry that lets BrambleGate's
// plugin/component set (both real CoreDNS-chain plugins and bramble-only
// components like mdnsadvertise) declare their static identity and report
// their current running state, so callers — chiefly the GUI — have one place
// to ask "is X loaded" and "is X a CoreDNS plugin" instead of per-component ad
// hoc nil-checks (dev-docs/plugin-system.md).
//
// Zero dependencies beyond stdlib is deliberate: plugins/localrecords and
// plugins/mdnsbridge must be able to import this package without pulling in
// model/store/the GUI (dev-docs/repo-layout.md's "plugins/* never depend on
// model" rule).
package pluginreg

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Kind distinguishes components wired into the CoreDNS directive chain from
// ones that run as their own host-process engine/goroutine and never appear
// in a Corefile (dev-docs/plugins.md).
type Kind string

const (
	CoreDNSPlugin Kind = "coredns"
	BrambleOnly   Kind = "bramble-only"
)

// Descriptor is a component's static identity, declared once (typically from
// an init()) and never changed afterward.
type Descriptor struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Required marks a component as structurally load-bearing — it cannot be
	// disabled by settings (localrecords owns home.arpa/DDR).
	Required bool `json:"required"`
	// DependsOn names other registered components this one relies on.
	// Advisory only: see Validate.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// State is a component's current runtime status, reported via SetLoaded.
type State struct {
	Loaded bool `json:"loaded"`
	// Reason is freeform, caller-supplied context: "" on a plain successful
	// load, "disabled in settings" for an intentional off, or
	// "failed to start: <err>" for a real failure. pluginreg does not parse
	// or branch on it.
	Reason string `json:"reason,omitempty"`
}

// Entry is a Descriptor plus its current State, as returned by All.
type Entry struct {
	Descriptor
	State
}

var (
	mu     sync.RWMutex
	descs  = map[string]Descriptor{}
	states = map[string]State{}
)

// Register declares a component's static identity. Safe to call from init();
// re-registering a name overwrites the previous descriptor.
func Register(d Descriptor) {
	mu.Lock()
	defer mu.Unlock()
	descs[d.Name] = d
}

// SetLoaded is the push side: a component (or whatever owns its start/stop,
// e.g. internal/gui/service.go) reports its own state whenever it starts,
// stops, errors, or is reconfigured.
func SetLoaded(name string, loaded bool, reason string) {
	mu.Lock()
	defer mu.Unlock()
	states[name] = State{Loaded: loaded, Reason: reason}
}

// Loaded reports whether name is currently loaded. Unregistered/never-reported
// names report false.
func Loaded(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return states[name].Loaded
}

// Get returns name's descriptor and current state. ok is false if name was
// never registered.
func Get(name string) (Descriptor, State, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := descs[name]
	return d, states[name], ok
}

// All returns every registered component with its current state, sorted by
// name for stable output (e.g. the GET /api/plugins debug endpoint).
func All() []Entry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, 0, len(descs))
	for name, d := range descs {
		out = append(out, Entry{Descriptor: d, State: states[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Validate checks the static graph — every DependsOn name was actually
// registered — and that every Required descriptor is currently Loaded. Intended
// to be called once, by cli, after the first successful engine/GUI-service
// startup: Required components (localrecords) only report Loaded once their
// own setup has actually run, so calling this at package-init time would
// always fail.
func Validate() error {
	mu.RLock()
	defer mu.RUnlock()

	var errs []error
	for _, d := range descs {
		for _, dep := range d.DependsOn {
			if _, ok := descs[dep]; !ok {
				errs = append(errs, fmt.Errorf("plugin %q depends on unregistered plugin %q", d.Name, dep))
			}
		}
		if d.Required && !states[d.Name].Loaded {
			errs = append(errs, fmt.Errorf("required plugin %q is not loaded", d.Name))
		}
	}
	return errors.Join(errs...)
}
