package mdnssd

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

var errNoTransport = errors.New("mdnssd: no Transport configured (use WithTransport)")

// Browser drives mDNS browses against a Transport.
type Browser struct {
	transport    Transport
	clock        Clock
	pollInterval time.Duration
}

// Option configures a Browser.
type Option func(*Browser)

// WithTransport sets the Transport a Browser uses. Required — New has no
// usable default until one is provided (transport_udp.go supplies the real
// one; tests use a fake).
func WithTransport(t Transport) Option { return func(b *Browser) { b.transport = t } }

// WithClock overrides the Clock (default: real time). Mainly for tests.
func WithClock(c Clock) Option { return func(b *Browser) { b.clock = c } }

// WithPollInterval sets how often the refresh/expiry cache is checked
// (default: 1s).
func WithPollInterval(d time.Duration) Option { return func(b *Browser) { b.pollInterval = d } }

// New returns a Browser. Pass WithTransport at minimum.
func New(opts ...Option) *Browser {
	b := &Browser{clock: realClock{}, pollInterval: time.Second}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Browse blocks until ctx is done, continuously browsing serviceType (e.g.
// "_http._tcp" — the ".local." domain is added automatically) and calling
// add/rmv as instances are discovered, updated, or expire. ifaceNames
// filters which interfaces' answers are accepted; nil/empty means all.
func (b *Browser) Browse(ctx context.Context, serviceType string, ifaceNames []string, add AddFunc, rmv RmvFunc) error {
	if b.transport == nil {
		return errNoTransport
	}
	question := strings.TrimSuffix(serviceType, ".") + ".local."
	state := newBrowserState(question, b.clock)

	allowed := make(map[string]bool, len(ifaceNames))
	for _, n := range ifaceNames {
		allowed[n] = true
	}

	if err := b.transport.SendQuery("", state.initialQuery()); err != nil {
		return err
	}

	inbound := b.transport.Read(ctx)
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-inbound:
			if !ok {
				return nil
			}
			if len(allowed) > 0 && !allowed[m.IfaceName] {
				continue
			}
			added, removed := state.Ingest(m.Msg, m.IfaceName, b.clock.Now())
			for _, e := range added {
				add(e)
			}
			for _, e := range removed {
				rmv(e)
			}
		case <-ticker.C:
			toQuery, removed := state.Tick(b.clock.Now())
			for _, q := range toQuery {
				_ = b.transport.SendQuery("", q)
			}
			for _, e := range removed {
				rmv(e)
			}
		}
	}
}

// browserState is the pure, synchronous core of browsing one service type:
// it consumes inbound messages and clock ticks, and decides what queries to
// (re)send and which Entry add/remove events to fire. Kept separate from
// I/O (Transport reads, real timers) so it's fully testable without
// goroutines or real time.
//
// Simplification (v1): TTL/refresh tracking (Cache) operates at the PTR
// (service-membership) level only — one record per discovered instance.
// SRV/TXT/A/AAAA data for an instance is kept as "latest known" and updated
// whenever fresh answers arrive, rather than separately TTL-tracked; a
// re-issued PTR query naturally elicits a fresh SRV/TXT/A/AAAA bundle from
// the responder. This still fixes dnssd #63 (silent eviction of live
// entries) without per-record-type refresh scheduling.
type browserState struct {
	question string
	typ      string
	domain   string

	cache   *Cache
	entries map[string]*Entry // keyed by instance name (PTR target)
}

func newBrowserState(question string, clock Clock) *browserState {
	typ, domain := splitServiceQuestion(question)
	return &browserState{
		question: question,
		typ:      typ,
		domain:   domain,
		cache:    NewCache(clock),
		entries:  map[string]*Entry{},
	}
}

// splitServiceQuestion splits a fully-qualified service question (e.g.
// "_http._tcp.local.") into its type and domain. mDNS is defined only for
// the "local." domain (RFC 6762 §3), so that's the only case handled.
func splitServiceQuestion(question string) (typ, domain string) {
	if strings.HasSuffix(question, ".local.") {
		return strings.TrimSuffix(question, ".local."), "local"
	}
	return strings.TrimSuffix(question, "."), ""
}

// instanceLabel strips the known service-question suffix from a PTR target
// to recover the instance's label, e.g. "Foo._http._tcp.local." with
// question "_http._tcp.local." yields "Foo".
func instanceLabel(instance, question string) string {
	return strings.TrimSuffix(strings.TrimSuffix(instance, question), ".")
}

// initialQuery returns the first query to send when a browse starts, with
// the QU (unicast-response) bit set per RFC 6762 §5.4.
func (b *browserState) initialQuery() *dns.Msg {
	return buildQuery(b.question, nil, true)
}

func (b *browserState) ensureEntry(instance, ifaceName string) *Entry {
	e, ok := b.entries[instance]
	if !ok {
		e = &Entry{
			Instance:  instanceLabel(instance, b.question),
			Type:      b.typ,
			Domain:    b.domain,
			IfaceName: ifaceName,
		}
		b.entries[instance] = e
	}
	return e
}

// Ingest processes one inbound message at time now, merging PTR/SRV/TXT/
// A/AAAA data into tracked instances and (re)storing PTR-driven cache
// entries. Returns instances that are resolvable and were touched by this
// message (added) — fired as a liveness heartbeat on every observation, not
// only when the entry's visible data changed, because a caller (like
// plugins/mdnsbridge's Table) may track its own independent liveness/TTL
// driven entirely by add calls; suppressing "unchanged" refreshes here
// would silently starve that tracking even though this record is still
// being actively kept alive underneath (this was a real regression: see the
// dnssd-63-style bug report this fix addresses). Also returns instances
// gone via a goodbye packet (TTL=0 PTR; removed).
func (b *browserState) Ingest(msg *dns.Msg, ifaceName string, now time.Time) (added, removed []Entry) {
	parsed := parseAnswers(msg)
	touched := make(map[string]bool)

	for _, ptr := range parsed.PTR {
		if ptr.Name != b.question {
			continue
		}
		if ptr.TTL <= 0 {
			b.cache.Remove(ptr.Ptr)
			if e, ok := b.entries[ptr.Ptr]; ok {
				removed = append(removed, *e)
				delete(b.entries, ptr.Ptr)
			}
			continue
		}
		b.cache.Store(ptr.Ptr, b.question, ptr.RR(), ptr.TTL)
		b.ensureEntry(ptr.Ptr, ifaceName).TTL = ptr.TTL
		touched[ptr.Ptr] = true
	}

	for _, srv := range parsed.SRV {
		if e, ok := b.entries[srv.Name]; ok {
			e.Host = strings.TrimSuffix(srv.Target, ".")
			touched[srv.Name] = true
		}
	}

	for _, txt := range parsed.TXT {
		if e, ok := b.entries[txt.Name]; ok {
			e.TXT = txt.Text
			touched[txt.Name] = true
		}
	}

	for _, a := range parsed.A {
		host := strings.TrimSuffix(a.Name, ".")
		for instance, e := range b.entries {
			if e.Host != "" && e.Host == host {
				e.IPv4 = mergeUniqueStr(e.IPv4, a.IP.String())
				touched[instance] = true
			}
		}
	}
	for _, aaaa := range parsed.AAAA {
		host := strings.TrimSuffix(aaaa.Name, ".")
		for instance, e := range b.entries {
			if e.Host != "" && e.Host == host {
				e.IPv6 = mergeUniqueStr(e.IPv6, aaaa.IP.String())
				touched[instance] = true
			}
		}
	}

	for instance := range touched {
		e, ok := b.entries[instance]
		if !ok || e.Host == "" || len(e.IPv4)+len(e.IPv6) == 0 {
			continue // not resolvable yet
		}
		added = append(added, *e)
	}

	return added, removed
}

// Tick advances the cache to now and reports queries that should be
// reissued (a record crossed a refresh threshold) and instances that should
// be reported removed (TTL fully elapsed with no refresh answer).
func (b *browserState) Tick(now time.Time) (toQuery []*dns.Msg, removed []Entry) {
	due, expired := b.cache.Tick(now)
	if len(due) > 0 {
		// All due records share this browse's question, so one re-query
		// covers every instance that needs refreshing this tick. Known
		// answers (RFC 6762 §7.1) tell the responder what we already have,
		// so it can skip re-sending records we don't need repeated.
		known := knownAnswers(b.cache.knownAnswersFor(b.question, now), b.question)
		toQuery = append(toQuery, buildQuery(b.question, known, false))
	}
	for _, instance := range expired {
		if e, ok := b.entries[instance]; ok {
			removed = append(removed, *e)
			delete(b.entries, instance)
		}
	}
	return toQuery, removed
}

func mergeUniqueStr(existing []string, add string) []string {
	for _, s := range existing {
		if s == add {
			return existing
		}
	}
	out := append(append([]string{}, existing...), add)
	sort.Strings(out)
	return out
}
