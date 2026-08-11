package vlancache

import (
	"net"
	"sync"
	"time"

	ccache "github.com/coredns/coredns/plugin/pkg/cache"
)

// maxScopedPerKey bounds how many distinct scope prefixes can accumulate for
// a single (qname, qtype, do, cd) — protects against unbounded growth if an
// upstream returns fine-grained (e.g. per-host, scope=32) SCOPE values.
const maxScopedPerKey = 16

// scopedEntry is one RFC 7871 SCOPE-derived cache entry: valid for requesters
// whose address falls inside prefix.
type scopedEntry struct {
	prefix *net.IPNet
	e      *entry
}

// store holds two tiers. direct is our own default assumption — entries
// keyed by the requester's VLAN bucket, used whenever the upstream gives no
// better information. scoped is populated only when an upstream actually
// echoes an RFC 7871 SCOPE PREFIX-LENGTH, and is checked first since it
// reflects the upstream's own authoritative statement of validity rather
// than our guess (see doc.go).
type store struct {
	direct *ccache.Cache[*entry]

	mu     sync.RWMutex
	scoped map[uint64][]*scopedEntry
}

func newStore(capacity int) *store {
	return &store{
		direct: ccache.New[*entry](capacity),
		scoped: map[uint64][]*scopedEntry{},
	}
}

func (s *store) getDirect(key uint64, now time.Time) (*entry, bool) {
	e, ok := s.direct.Get(key)
	if !ok || e.remaining(now) <= 0 {
		return nil, false
	}
	return e, true
}

func (s *store) setDirect(key uint64, e *entry) {
	s.direct.Add(key, e)
}

// getScoped returns the most specific (longest-prefix) non-expired scoped
// entry under key that contains ip, if any.
func (s *store) getScoped(key uint64, ip net.IP, now time.Time) (*entry, bool) {
	if ip == nil {
		return nil, false
	}
	s.mu.RLock()
	entries := s.scoped[key]
	s.mu.RUnlock()

	var best *scopedEntry
	bestLen := -1
	for _, se := range entries {
		if se.e.remaining(now) <= 0 {
			continue
		}
		if !se.prefix.Contains(ip) {
			continue
		}
		l, _ := se.prefix.Mask.Size()
		if l > bestLen {
			best, bestLen = se, l
		}
	}
	if best == nil {
		return nil, false
	}
	return best.e, true
}

// setScoped stores e under prefix, replacing any existing entry for the same
// prefix. Once maxScopedPerKey is reached, the oldest entry is dropped to
// make room — a simple bound, not an LRU; scoped growth is expected to be
// rare (it only happens when an upstream cooperates with RFC 7871 scope).
func (s *store) setScoped(key uint64, prefix *net.IPNet, e *entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.scoped[key]
	for i, se := range entries {
		if se.prefix.String() == prefix.String() {
			entries[i] = &scopedEntry{prefix: prefix, e: e}
			return
		}
	}
	if len(entries) >= maxScopedPerKey {
		entries = entries[1:]
	}
	s.scoped[key] = append(entries, &scopedEntry{prefix: prefix, e: e})
}
