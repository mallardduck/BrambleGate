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

	// epoch counts flushes. A fetch records it before asking the upstream and
	// stores its answer through ifCurrent, so an answer that was already in
	// flight when a flush landed is dropped instead of re-populating the
	// cache with the data the flush meant to evict. epochMu makes the
	// check-and-store atomic against a flush: a store either completes
	// before the flush's walk (and is walked) or sees the new epoch and skips.
	epochMu sync.RWMutex
	epoch   uint64
}

func newStore(capacity int) *store {
	return &store{
		direct: ccache.New[*entry](capacity),
		scoped: map[uint64][]*scopedEntry{},
	}
}

// getDirect returns the entry for key, if any within its TTL or, when
// staleTTL > 0, within staleTTL past expiry. fresh reports which case
// applied — callers use it to decide between a normal hit and a
// serve-stale-and-refresh-in-background response (see handler.go).
func (s *store) getDirect(key uint64, now time.Time, staleTTL time.Duration) (e *entry, fresh, ok bool) {
	e, ok = s.direct.Get(key)
	if !ok {
		return nil, false, false
	}
	rem := e.remaining(now)
	if rem > 0 {
		return e, true, true
	}
	if staleTTL > 0 && -rem <= staleTTL {
		return e, false, true
	}
	return nil, false, false
}

func (s *store) setDirect(key uint64, e *entry) {
	s.direct.Add(key, e)
}

// generation returns the current flush epoch; see store.epoch.
func (s *store) generation() uint64 {
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	return s.epoch
}

// invalidate starts a new flush epoch. It must run before the flush walks
// the tiers, so in-flight fetches that finish afterwards skip their store.
func (s *store) invalidate() {
	s.epochMu.Lock()
	s.epoch++
	s.epochMu.Unlock()
}

// ifCurrent runs fn (a set*) only if no flush has happened since gen was read
// from generation.
func (s *store) ifCurrent(gen uint64, fn func()) {
	s.epochMu.RLock()
	defer s.epochMu.RUnlock()
	if s.epoch == gen {
		fn()
	}
}

// getScoped returns the most specific (longest-prefix) scoped entry under
// key that contains ip, if any — preferring a still-fresh entry over a
// stale one outright, and the longest prefix within whichever freshness
// class wins, matching getDirect's fresh/stale distinction.
func (s *store) getScoped(key uint64, ip net.IP, now time.Time, staleTTL time.Duration) (e *entry, fresh, ok bool) {
	if ip == nil {
		return nil, false, false
	}
	s.mu.RLock()
	entries := s.scoped[key]
	s.mu.RUnlock()

	var best *scopedEntry
	bestLen := -1
	bestFresh := false
	for _, se := range entries {
		rem := se.e.remaining(now)
		entryFresh := rem > 0
		if !entryFresh && (staleTTL <= 0 || -rem > staleTTL) {
			continue // expired beyond the stale grace window (or stale serving is off)
		}
		if !se.prefix.Contains(ip) {
			continue
		}
		l, _ := se.prefix.Mask.Size()
		if best == nil || (entryFresh && !bestFresh) || (entryFresh == bestFresh && l > bestLen) {
			best, bestLen, bestFresh = se, l, entryFresh
		}
	}
	if best == nil {
		return nil, false, false
	}
	return best.e, bestFresh, true
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

// clear drops every entry from both tiers and returns how many were removed.
// Fetches already in flight are invalidated (see store.epoch) so they can't
// re-store a pre-flush answer; fetches started afterwards populate normally.
func (s *store) clear() int {
	s.invalidate()
	n := 0
	s.direct.Walk(func(items map[uint64]*entry, k uint64) bool {
		delete(items, k)
		n++
		return true
	})
	s.mu.Lock()
	for k, entries := range s.scoped {
		n += len(entries)
		delete(s.scoped, k)
	}
	s.mu.Unlock()
	return n
}

// clearName drops every entry (all qtypes, VLAN buckets, scopes, DO/CD
// variants) whose question name is exactly qname, which must already be
// canonical. Subdomains are not matched. Returns how many were removed.
func (s *store) clearName(qname string) int {
	s.invalidate()
	n := 0
	s.direct.Walk(func(items map[uint64]*entry, k uint64) bool {
		if e := items[k]; e != nil && e.qname == qname {
			delete(items, k)
			n++
		}
		return true
	})
	s.mu.Lock()
	for k, entries := range s.scoped {
		kept := entries[:0]
		for _, se := range entries {
			if se.e.qname == qname {
				n++
				continue
			}
			kept = append(kept, se)
		}
		if len(kept) == 0 {
			delete(s.scoped, k)
		} else {
			s.scoped[k] = kept
		}
	}
	s.mu.Unlock()
	return n
}
