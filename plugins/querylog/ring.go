package querylog

import (
	"strings"
	"sync"
)

// Filter narrows a Snapshot to matching entries. A zero-value field means "no
// constraint on that dimension" — Filter{} matches every entry. QName/Client
// are case-insensitive substring matches (docs/query-log.md: "qname substring
// ... + client/VLAN"); VLAN is an exact match, since VLAN names are a closed,
// exact set from settings.yaml rather than free text.
type Filter struct {
	QName  string
	Client string
	VLAN   string
	// QType is an exact match against Entry.QType; 0 means "no constraint" —
	// same convention as the other zero-value fields, safe since a real
	// query never has qtype 0.
	QType uint16
}

func (f Filter) matches(e Entry) bool {
	if f.QName != "" && !containsFold(e.QName, f.QName) {
		return false
	}
	if f.Client != "" && !containsFold(e.Client.IP, f.Client) {
		return false
	}
	if f.VLAN != "" && e.Client.VLAN != f.VLAN {
		return false
	}
	if f.QType != 0 && e.QType != f.QType {
		return false
	}
	return true
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Ring is a fixed-capacity, count-based ring buffer of Entry — deliberately
// not time-windowed, since a time window is unbounded under sustained QPS
// (docs/query-log.md). One shared instance serves every VLAN; "show VLAN X"
// is a Filter, not a separate ring per VLAN.
type Ring struct {
	mu   sync.RWMutex
	buf  []Entry
	head int // index the next Push writes to
	size int // number of valid entries currently in buf, <= len(buf)
}

// NewRing returns a Ring holding at most capacity entries. capacity must be
// positive — callers configure it from settings, never 0.
func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]Entry, capacity)}
}

// Cap returns the ring's fixed entry capacity.
func (r *Ring) Cap() int {
	return len(r.buf)
}

// Push appends e, evicting the oldest entry once the ring is at capacity.
func (r *Ring) Push(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.head] = e
	r.head = (r.head + 1) % len(r.buf)
	if r.size < len(r.buf) {
		r.size++
	}
}

// Snapshot returns entries matching f, newest first. The returned slice is a
// copy — safe to use after the call without holding any lock.
func (r *Ring) Snapshot(f Filter) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Entry, 0, r.size)
	// head-1 is the most recently written slot; walk backward r.size steps.
	for i := range r.size {
		idx := (r.head - 1 - i + len(r.buf)) % len(r.buf)
		e := r.buf[idx]
		if f.matches(e) {
			out = append(out, e)
		}
	}
	return out
}
