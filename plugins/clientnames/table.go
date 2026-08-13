// Package clientnames answers "who is 192.168.10.47" for the query log and
// GET /api/clients (dev-docs/client-names.md), the way Pi-hole's client-name
// resolution does — without a DHCP tier, since BrambleGate isn't a DHCP
// server. It is never a CoreDNS-chain plugin: like plugins/mdnsadvertise, it
// only builds and exposes a cache, owned by the host process for its whole
// lifetime (internal/gui/service.go).
//
// Three tiers, in priority order: a static hosts.yaml entry (tier 0, read
// live off an injected index), a live address match against mdnsbridge's
// discovery table (tier 1, also read live, never cached), and a cached PTR
// query against an optional configured upstream (tier 2, the only tier that
// costs a real network round trip and so the only one actually cached).
package clientnames

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mallardduck/BrambleGate/pluginreg"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
)

func init() {
	pluginreg.Register(pluginreg.Descriptor{
		Name: "clientnames",
		Kind: pluginreg.BrambleOnly,
		// Advisory only (dev-docs/plugin-system.md): tier 0 reads the hosts
		// plugin's generated data, tier 1 reads mdnsbridge's Table directly.
		DependsOn: []string{"hosts", "mdnsbridge"},
	})
}

// resolveQueueSize bounds the async tier-2 (PTR) resolution queue Observe
// feeds — deliberately small and best-effort: a burst of new client IPs
// (e.g. right after boot) may drop a few first-sight PTR attempts rather than
// block the request hot path or grow unbounded. A dropped IP just stays
// SourceNone until Sweep's next hourly pass, or a future query resolves it.
const resolveQueueSize = 64

// ptrTimeout bounds a single PTR round trip so one unreachable ptr_upstream
// can't stall the resolve worker indefinitely.
const ptrTimeout = 2 * time.Second

// Table is the in-memory client cache, safe for concurrent use by the
// passive querylog observer hook (writes/queues), the resolve worker
// (writes), and the GUI/API (reads).
type Table struct {
	mu       sync.Mutex
	entries  map[string]*Entry
	cfg      Config
	now      func() time.Time
	resolveQ chan string
}

// NewTable returns a Table with the given resolution config. Nothing is
// resolved until Observe/Run are used.
func NewTable(cfg Config) *Table {
	return &Table{
		entries:  map[string]*Entry{},
		cfg:      cfg,
		now:      time.Now,
		resolveQ: make(chan string, resolveQueueSize),
	}
}

// SetConfig replaces the resolution config (e.g. after a settings/hosts
// change) — takes effect on the next resolve, live.
func (t *Table) SetConfig(cfg Config) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg = cfg
}

// Observe records a client IP as seen. On first sight it queues a tier-2
// (PTR) resolution attempt — but only if tier 0/1 didn't already resolve it
// live, since PTR is "fallback for client IPs tier 1 didn't match"
// (dev-docs/client-names.md). Never blocks: the queue send is non-blocking
// and PTR resolution itself happens on Run's background worker, off the
// caller's goroutine (typically querylog's request path).
func (t *Table) Observe(ip string) {
	if ip == "" {
		return
	}
	now := t.now()
	t.mu.Lock()
	e, existed := t.entries[ip]
	if !existed {
		t.entries[ip] = &Entry{IP: ip, LastSeen: now}
	} else {
		e.LastSeen = now
	}
	resolver := t.cfg.Resolver
	t.mu.Unlock()

	if existed || resolver == nil {
		return
	}
	if _, src := t.resolveLive(ip); src != SourceNone {
		return
	}
	select {
	case t.resolveQ <- ip:
	default: // queue full: best-effort, see resolveQueueSize
	}
}

// Resolve answers "who is ip" right now: hosts (tier 0) -> live mdnsbridge
// match (tier 1) -> cached PTR result (tier 2). source is "" when nothing has
// resolved a name for ip.
func (t *Table) Resolve(ip string) (hostname string, source string) {
	if name, src := t.resolveLive(ip); src != SourceNone {
		return name, string(src)
	}
	t.mu.Lock()
	e, ok := t.entries[ip]
	t.mu.Unlock()
	if ok && e.Source == SourcePTR {
		return e.Hostname, string(SourcePTR)
	}
	return "", ""
}

// resolveLive checks tiers 0 and 1 only — the two that are read live, never
// cached in an Entry.
func (t *Table) resolveLive(ip string) (string, Source) {
	t.mu.Lock()
	hostsIdx := t.cfg.HostsIndex
	mdnsTbl := t.cfg.MDNS
	t.mu.Unlock()

	if name, ok := hostsIdx[ip]; ok && name != "" {
		return name, SourceHosts
	}
	if mdnsTbl != nil {
		if host, ok := matchMDNS(mdnsTbl, ip); ok {
			return host, SourceMDNS
		}
	}
	return "", SourceNone
}

// matchMDNS asks "does any mdnsbridge.Table entry carry this exact address"
// — an address match, not a name import (dev-docs/client-names.md: the Table
// holds devices announcing services, a different population than "IPs that
// have sent us a DNS query").
func matchMDNS(tbl *mdnsbridge.Table, ip string) (string, bool) {
	for _, e := range tbl.Snapshot() {
		if containsIP(e.IPv4, ip) || containsIP(e.IPv6, ip) {
			if host := strings.TrimSuffix(e.Host, "."); host != "" {
				return host, true
			}
		}
	}
	return "", false
}

func containsIP(list []string, ip string) bool {
	target := net.ParseIP(ip)
	for _, s := range list {
		if target != nil {
			if got := net.ParseIP(s); got != nil && got.Equal(target) {
				return true
			}
			continue
		}
		if s == ip {
			return true
		}
	}
	return false
}

// Snapshot returns every known client IP, sorted, with Hostname/Source
// reflecting a live resolve — never stale hosts/mDNS data, even though
// they're not what's persisted in the Entry itself (dev-docs/client-names.md:
// only tier 2 is cached).
func (t *Table) Snapshot() []Entry {
	t.mu.Lock()
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	t.mu.Unlock()

	for i := range out {
		if name, src := t.resolveLive(out[i].IP); src != SourceNone {
			out[i].Hostname, out[i].Source = name, src
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

// Run services the async PTR resolve queue and the hourly re-sweep
// (Sweep) until ctx is canceled. Owned by the host process the same way
// mdnsbridge.NewListener's Run is (internal/gui/service.go), so it survives
// independent of any single DNS request's lifetime.
func (t *Table) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-t.resolveQ:
			t.attemptPTR(ip)
		case <-ticker.C:
			t.Sweep()
		}
	}
}

// attemptPTR performs one tier-2 lookup and, on success, caches it onto the
// existing Entry (a no-op if the IP was evicted/never observed).
func (t *Table) attemptPTR(ip string) {
	t.mu.Lock()
	resolver := t.cfg.Resolver
	t.mu.Unlock()
	if resolver == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ptrTimeout)
	defer cancel()
	host, err := resolver.Lookup(ctx, ip)
	if err != nil || host == "" {
		return
	}
	t.mu.Lock()
	if e, ok := t.entries[ip]; ok {
		e.Hostname = host
		e.Source = SourcePTR
		e.ResolvedAt = t.now()
	}
	t.mu.Unlock()
}

// Sweep re-resolves every PTR-sourced entry per the configured
// RefreshHostnames mode, returning how many entries it attempted. ipv4_only
// (the default) skips IPv6 entries — SLAAC/Privacy-Extension addresses
// rotate too fast for a re-poll to be worth the query, same reasoning as
// Pi-hole's default; "none" skips the sweep entirely (resolve once, never
// re-check).
func (t *Table) Sweep() int {
	t.mu.Lock()
	resolver := t.cfg.Resolver
	mode := t.cfg.RefreshHostnames
	if mode == "" {
		mode = "ipv4_only"
	}
	var ips []string
	if resolver != nil && mode != "none" {
		for ip, e := range t.entries {
			if e.Source != SourcePTR {
				continue
			}
			if mode == "ipv4_only" && strings.Contains(ip, ":") {
				continue
			}
			ips = append(ips, ip)
		}
	}
	t.mu.Unlock()

	for _, ip := range ips {
		t.attemptPTR(ip)
	}
	return len(ips)
}
