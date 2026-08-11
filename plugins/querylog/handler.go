package querylog

import (
	"context"
	"net"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// defaultCacheLatencyThreshold is the fallback classification boundary
// between "cache" and "forward" when no downstream plugin self-attributed
// (see classifyFallback). A cache hit never leaves this process, so it is
// consistently much faster than a real network round trip to forward's
// upstream — this is a heuristic, not an exact boundary; revisit if it
// misclassifies against a real upstream in practice (dev-docs/query-log.md).
const defaultCacheLatencyThreshold = 2 * time.Millisecond

// QueryLog observes every query passing through the CoreDNS chain, regardless
// of which plugin answers it, and records a completed Entry to Ring. See
// dev-docs/query-log.md.
type QueryLog struct {
	Next plugin.Handler
	Ring *Ring
	// Store is the durable persistence layer (Phase 7b) — nil when
	// persistence isn't configured (e.g. a bare "querylog" stanza with no
	// "db" sub-directive, as in older/unit-test Corefiles). Record is a
	// nil-safe no-op, so this field never needs a nil check at the call
	// site.
	Store *Store
	VLANs vlanmatch.Table

	// Now, if set, replaces time.Now for measuring Latency — overridable so
	// tests get deterministic cache/forward fallback classification instead
	// of depending on real wall-clock timing.
	Now func() time.Time
	// CacheLatencyThreshold, if positive, replaces defaultCacheLatencyThreshold.
	CacheLatencyThreshold time.Duration
}

func (q *QueryLog) Name() string { return "querylog" }

// ServeDNS stashes a mutable *Entry in ctx before calling Next, so downstream
// plugins that self-attribute (localrecords, mdnsbridge) can set Source/
// Verdict directly. It wraps w in a dnstest.Recorder to observe the rcode
// actually written, since forward/cache don't self-attribute and querylog
// must infer their Source/Verdict itself from the completed round trip.
func (q *QueryLog) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	start := q.now()

	vlan, _ := q.VLANs.Lookup(net.ParseIP(state.IP()))
	entry := &Entry{
		Timestamp: start,
		Client:    ClientInfo{IP: state.IP(), VLAN: vlan},
		QName:     state.Name(),
		QType:     state.QType(),
		Listener:  state.LocalAddr(),
		Proto:     state.Proto(),
	}
	ctx = NewContext(ctx, entry)

	rec := dnstest.NewRecorder(w)
	rcode, err := plugin.NextOrFailure(q.Name(), q.Next, ctx, rec, r)
	if rec.Msg == nil && !plugin.ClientWrite(rcode) {
		rec.Rcode = rcode
	}

	entry.Latency = q.now().Sub(start)
	entry.Rcode = rec.Rcode
	if entry.Source == "" {
		entry.Verdict, entry.Source = classifyFallback(rec.Rcode, entry.Latency, q.cacheLatencyThreshold())
	}
	if rec.Msg != nil {
		entry.AuthenticatedData = rec.Msg.AuthenticatedData
		if len(rec.Msg.Answer) > 0 {
			entry.AnswerType = dns.TypeToString[rec.Msg.Answer[0].Header().Rrtype]
		}
	}
	q.Ring.Push(*entry)
	q.Store.Record(*entry)
	globalStats.observe(*entry)

	return rcode, err
}

func (q *QueryLog) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *QueryLog) cacheLatencyThreshold() time.Duration {
	if q.CacheLatencyThreshold > 0 {
		return q.CacheLatencyThreshold
	}
	return defaultCacheLatencyThreshold
}

// classifyFallback infers Source/Verdict for the forward/cache path when no
// downstream plugin self-attributed (dev-docs/query-log.md: "forward/cache
// don't participate — querylog fills in Source ... from rcode/timing, since
// it wraps the full round trip").
func classifyFallback(rcode int, latency, cacheLatencyThreshold time.Duration) (verdict, source string) {
	if rcode == dns.RcodeNameError {
		return "nxdomain", "forward"
	}
	if latency < cacheLatencyThreshold {
		return "cached", "cache"
	}
	return "forwarded", "forward"
}
