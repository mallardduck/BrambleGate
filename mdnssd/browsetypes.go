package mdnssd

import (
	"context"
	"time"

	"github.com/miekg/dns"
)

// metaServiceType is the DNS-SD meta-query (RFC 6763 §9): browsing it
// enumerates which service types are actually being advertised, rather than
// requiring the caller to already know what to look for. This is the
// primitive dnssd issue #20 asked for and never got — dnssd.LookupType
// can't be pointed at it, because it filters cache entries by exact
// ServiceName() match against the queried name, which the meta-query's PTR
// answers (whose target IS the discovered type, not an instance under it)
// never satisfy.
const metaServiceType = "_services._dns-sd._udp"

// BrowseTypes blocks until ctx is done, continuously discovering service
// types advertised on the network (via the meta-query above, domain
// "local") and calling onType once per distinct type found (e.g.
// "_http._tcp"). ifaceNames filters which interfaces' answers are accepted;
// nil/empty means all.
func (b *Browser) BrowseTypes(ctx context.Context, ifaceNames []string, onType TypeFunc) error {
	if b.transport == nil {
		return errNoTransport
	}
	state := newTypeBrowserState("local", b.clock)

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
			for _, typ := range state.Ingest(m.Msg, b.clock.Now()) {
				onType(typ)
			}
		case <-ticker.C:
			for _, q := range state.Tick(b.clock.Now()) {
				_ = b.transport.SendQuery("", q)
			}
		}
	}
}

// typeBrowserState is the pure, synchronous core of BrowseTypes — see
// browserState's doc comment for why this is kept separate from I/O.
type typeBrowserState struct {
	question string // fully-qualified meta-query name, e.g. "_services._dns-sd._udp.local."
	cache    *Cache
	seen     map[string]bool // discovered types already reported
}

func newTypeBrowserState(domain string, clock Clock) *typeBrowserState {
	question := metaServiceType + "." + domain + "."
	return &typeBrowserState{question: question, cache: NewCache(clock), seen: map[string]bool{}}
}

func (t *typeBrowserState) initialQuery() *dns.Msg {
	return buildQuery(t.question, nil, true)
}

// Ingest processes one inbound message, returning types discovered for the
// first time (each type is reported at most once per browse).
func (t *typeBrowserState) Ingest(msg *dns.Msg, now time.Time) []string {
	parsed := parseAnswers(msg)

	var newTypes []string
	for _, ptr := range parsed.PTR {
		if ptr.Name != t.question {
			continue
		}
		if ptr.TTL <= 0 {
			t.cache.Remove(ptr.Ptr)
			continue // no removal semantics for types; just stop refreshing it
		}
		t.cache.Store(ptr.Ptr, t.question, ptr.TTL)

		typ, _ := splitServiceQuestion(ptr.Ptr)
		if !t.seen[typ] {
			t.seen[typ] = true
			newTypes = append(newTypes, typ)
		}
	}
	return newTypes
}

// Tick advances the cache to now and reports a re-query if any discovered
// type's PTR is due for refresh (RFC 6762 §5.2) — one query covers all of
// them, since every record shares this browse's meta-query question.
func (t *typeBrowserState) Tick(now time.Time) []*dns.Msg {
	due, _ := t.cache.Tick(now)
	if len(due) > 0 {
		return []*dns.Msg{buildQuery(t.question, nil, false)}
	}
	return nil
}
