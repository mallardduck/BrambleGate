package querylog

import "sync/atomic"

// clientObserver, if set, is called with every observed client IP+VLAN — the
// passive discovery feed plugins/clientnames subscribes to
// (dev-docs/client-names.md: "every ClientInfo.IP already flows through
// querylog ... clientnames subscribes to that stream"). VLAN travels
// alongside the IP because clientnames' PTR tier picks its target per VLAN
// (dev-docs/client-names.md's gateway auto-detection). querylog's module
// deliberately doesn't depend on clientnames (dev-docs/repo-layout.md: only
// internal/gui/service.go and internal/cli, both in the root module, know
// about both packages), so this is a func-var hook set from there — same
// shape as SetHostNames.
var clientObserver atomic.Pointer[func(ip, vlan string)]

// SetClientObserver replaces the passive client-IP observer. Pass nil to
// disable (e.g. client_names.enabled is off) — ServeDNS's call becomes a
// nil-safe no-op.
func SetClientObserver(f func(ip, vlan string)) {
	if f == nil {
		clientObserver.Store(nil)
		return
	}
	clientObserver.Store(&f)
}

// observeClient calls the current observer, if any, with ip/vlan. No-op
// when ip is empty or no observer is set.
func observeClient(ip, vlan string) {
	if ip == "" {
		return
	}
	if p := clientObserver.Load(); p != nil {
		(*p)(ip, vlan)
	}
}
