package querylog

import (
	"strings"
	"sync/atomic"
)

// hostNames is the process-wide set of names answered by the stock CoreDNS
// hosts plugin, per the current hosts.yaml — refreshed by internal/cli on
// every load/reload (same "set before engine.New, refreshed again in
// reloadFn" pattern as vlanmatch.Current(), since hosts.yaml lives in
// model, which querylog deliberately doesn't import — dev-docs/query-log.md
// / dev-docs/repo-layout.md).
//
// It exists because the stock hosts plugin (unlike localrecords/mdnsbridge)
// is third-party code that can't self-attribute via querylog's context: it
// has no idea querylog exists. Left alone, ServeDNS's classifyFallback
// would misread a hosts answer's near-zero in-process latency as a cache
// hit. hosts runs first in the chain (internal/engine/directives.go) and
// unconditionally intercepts any name a user explicitly listed, so a name
// match here is a reliable substitute for self-attribution — no latency
// heuristic needed.
var hostNames atomic.Pointer[map[string]struct{}]

// SetHostNames replaces the current name set from the hostname/alias list
// in hosts.yaml. Names need not be pre-normalized; each is lower-cased and
// FQDN-qualified the same way isHostName normalizes a query name before
// comparing.
func SetHostNames(names []string) {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[normalizeHostName(n)] = struct{}{}
	}
	hostNames.Store(&set)
}

// isHostName reports whether qname matches an entry set by SetHostNames.
func isHostName(qname string) bool {
	p := hostNames.Load()
	if p == nil {
		return false
	}
	_, ok := (*p)[normalizeHostName(qname)]
	return ok
}

func normalizeHostName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}
