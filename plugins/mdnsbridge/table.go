// Package mdnsbridge observes mDNS announcements and makes discovered hostnames
// resolvable as normal DNS answers, with a selector-based publish/promote model
// (docs/plugins.md).
//
// The live discovery Table and its listener are owned by the host process (cli),
// not by the plugin's OnStartup: mDNS churn must survive engine reloads, and the
// GUI shares the same Table directly (single process, no IPC). The plugin is a
// thin DNS overlay that resolves queries against the injected Table.
package mdnsbridge

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is the fallback staleness window used only for an entry with
// no real per-record TTL (e.g. one a test seeded directly, or any future
// caller that doesn't have one to offer). Deliberately generous: the common
// case is a real TTL, propagated end-to-end from the mDNS record's own
// announced TTL (see Entry.TTL and mdnsquery.Entry.TTL) — that's the actual
// liveness window the device advertised, and mdnssd's own active refresh
// (RFC 6762 §5.2) already keeps it current via repeated Upsert calls. This
// constant is a defensive backstop for if that stops happening (e.g. the
// browse goroutine gets stuck), not a second, independently-invented source
// of truth — it must not be short enough to expire an entry mdnssd is still
// correctly refreshing underneath.
const DefaultTTL = 24 * time.Hour

// Entry is one discovered DNS-SD instance. It is keyed by (service, instance,
// host); a host advertising multiple services yields multiple entries that share
// addresses. Name is the mapped DNS name for auto-published serving.
type Entry struct {
	Name      string            `json:"name"`     // mapped DNS name, e.g. "printer.home.arpa."
	Host      string            `json:"host"`     // mDNS host, e.g. "printer.local."
	Service   string            `json:"service"`  // e.g. "_http._tcp"
	Instance  string            `json:"instance"` // instance label
	TXT       map[string]string `json:"txt,omitempty"`
	IPv4      []string          `json:"ipv4,omitempty"`
	IPv6      []string          `json:"ipv6,omitempty"`
	Published bool              `json:"published"`
	LastSeen  time.Time         `json:"last_seen"`
	// TTL is the record's own most recently announced TTL (0 = unknown).
	// Table.expired prefers this over the table-wide default — see DefaultTTL.
	TTL time.Duration `json:"ttl,omitempty"`

	manual bool // GUI approved this entry regardless of auto-publish selectors
}

// Table is the in-memory discovery table, safe for concurrent use by the mDNS
// listener (writes), the DNS handler (reads), and the GUI (reads + approvals).
type Table struct {
	mu      sync.RWMutex
	entries map[string]*Entry // keyed by service|instance|host
	cfg     Config
	ttl     time.Duration
	now     func() time.Time
}

// NewTable returns a Table with the given resolution config.
func NewTable(cfg Config, ttl time.Duration) *Table {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Table{entries: map[string]*Entry{}, cfg: cfg, ttl: ttl, now: time.Now}
}

// SetConfig replaces the resolution config and re-derives Name/Published for all
// current entries (e.g. after a settings/records change).
func (t *Table) SetConfig(cfg Config) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg = cfg
	for _, e := range t.entries {
		t.rederive(e)
	}
}

func entryKey(e Entry) string {
	return strings.ToLower(e.Service + "|" + e.Instance + "|" + strings.TrimSuffix(e.Host, ".") + ".")
}

func fqdn(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) + "." }

// Upsert records a discovery, (re)deriving its mapped name and published state.
// Manual approval and last-seen are preserved across re-announcements.
func (t *Table) Upsert(e Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := entryKey(e)
	now := t.now()
	if cur, ok := t.entries[k]; ok {
		cur.IPv4 = mergeUnique(cur.IPv4, e.IPv4)
		cur.IPv6 = mergeUnique(cur.IPv6, e.IPv6)
		if len(e.TXT) > 0 {
			cur.TXT = e.TXT
		}
		if e.TTL > 0 {
			cur.TTL = e.TTL
		}
		cur.LastSeen = now
		t.rederive(cur)
		return
	}
	e.LastSeen = now
	t.rederive(&e)
	t.entries[k] = &e
}

// rederive recomputes the entry's mapped Name and Published state from config.
func (t *Table) rederive(e *Entry) {
	label := firstLabel(e.Host)
	if label != "" {
		e.Name = label + "." + strings.TrimSuffix(t.cfg.suffixFor(*e), ".") + "."
	}
	e.Published = e.manual || t.cfg.AutoPublish.MatchAny(*e, t.cfg.VLANs)
}

// Resolve answers a query name from the table. owned=true means the name is the
// plugin's to answer authoritatively (a promoted binding, or a live published
// name) — the caller returns the addresses or NODATA rather than falling through.
// owned=false means fall through to the rest of the chain.
func (t *Table) Resolve(qname string) (ipv4, ipv6 []string, owned bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	name := fqdn(qname)

	// Promoted binding: authoritative even when no device currently matches.
	if sel, ok := t.cfg.Promoted[name]; ok {
		v4, v6 := t.collect(func(e *Entry) bool { return sel.Match(*e, t.cfg.VLANs) })
		return v4, v6, true
	}

	// Auto-/manually-published discovery served under its mapped name.
	v4, v6 := t.collect(func(e *Entry) bool { return e.Published && e.Name == name })
	if len(v4)+len(v6) > 0 {
		return v4, v6, true
	}
	return nil, nil, false
}

// collect merges addresses from all non-expired entries matching pred.
func (t *Table) collect(pred func(*Entry) bool) (ipv4, ipv6 []string) {
	for _, e := range t.entries {
		if t.expired(e) || !pred(e) {
			continue
		}
		ipv4 = mergeUnique(ipv4, e.IPv4)
		ipv6 = mergeUnique(ipv6, e.IPv6)
	}
	return ipv4, ipv6
}

// Snapshot returns all non-expired entries, sorted, for the GUI.
func (t *Table) Snapshot() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		if !t.expired(e) {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Service < out[j].Service
	})
	return out
}

// SetPublished manually approves/unapproves entries served under a mapped name
// (GUI). Returns false if no entry maps to that name.
func (t *Table) SetPublished(name string, published bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	target := fqdn(name)
	found := false
	for _, e := range t.entries {
		if e.Name == target {
			e.manual = published
			t.rederive(e)
			found = true
		}
	}
	return found
}

// Remove deletes an entry by its service/instance/host key. Returns true if an entry
// was deleted, false if no match was found. Called when a goodbye packet is received
// or an entry is explicitly removed.
func (t *Table) Remove(service, instance, host string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := Entry{Service: service, Instance: instance, Host: host}
	k := entryKey(e)
	if _, ok := t.entries[k]; ok {
		delete(t.entries, k)
		return true
	}
	return false
}

// Clear drops every discovered entry immediately, returning how many were
// dropped. Used when the browse configuration itself changes (service
// types / interfaces): entries discovered under the old filter are no
// longer being refreshed by the new browse, and would otherwise linger
// until their TTL naturally expires instead of disappearing right away.
func (t *Table) Clear() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	dropped := len(t.entries)
	t.entries = map[string]*Entry{}
	return dropped
}

// Expire drops entries not re-announced within the TTL, returning how many
// were dropped.
func (t *Table) Expire() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	dropped := 0
	for k, e := range t.entries {
		if t.expired(e) {
			delete(t.entries, k)
			dropped++
		}
	}
	return dropped
}

// expired uses the entry's own real TTL when known, falling back to the
// table-wide DefaultTTL only when it isn't — see DefaultTTL's doc comment
// for why the real value must take priority.
func (t *Table) expired(e *Entry) bool {
	ttl := t.ttl
	if e.TTL > 0 {
		ttl = e.TTL
	}
	return t.now().Sub(e.LastSeen) > ttl
}

func firstLabel(host string) string {
	h := strings.ToLower(strings.Trim(host, "."))
	if i := strings.Index(h, "."); i >= 0 {
		h = h[:i]
	}
	return sanitizeLabel(h)
}

func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
