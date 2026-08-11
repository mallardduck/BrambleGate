package vlancache

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnsutil"
	"github.com/coredns/coredns/plugin/pkg/response"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"golang.org/x/sync/singleflight"

	"github.com/mallardduck/BrambleGate/plugins/querylog"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// errNoUpstreamResponse guards against a Next handler that returns without
// ever calling WriteMsg — shouldn't happen with well-behaved plugins, but
// singleflight's shared result must be an error, not a nil *dns.Msg.
var errNoUpstreamResponse = errors.New("vlancache: upstream produced no response")

// globalBucket is the direct-tier VLAN key used when the requester matched
// no declared VLAN (or none are declared at all) — every such requester
// shares one entry, the same behavior as the stock cache plugin with ECS
// off.
const globalBucket = ""

const (
	minTTL  = dnsutil.MinimalDefaultTTL
	maxTTL  = dnsutil.MaximumDefaultTTL
	minNTTL = dnsutil.MinimalDefaultTTL
	maxNTTL = dnsutil.MaximumDefaultTTL / 2

	// defaultFailTTL/maxFailTTL mirror the stock cache plugin's SERVFAIL
	// handling: a short default, capped at 5 minutes per RFC 2308.
	defaultFailTTL = dnsutil.MinimalDefaultTTL
	maxFailTTL     = 5 * time.Minute

	defaultCap = 10000
)

// VlanCache is a CoreDNS cache plugin. See doc.go for the design.
type VlanCache struct {
	Next plugin.Handler

	vlans   vlanmatch.Table
	store   *store
	failTTL time.Duration

	// sf coalesces concurrent misses sharing a key into one upstream call —
	// see fetch. Without this, a burst of clients asking an identical
	// question while the first answer is still in flight would all miss the
	// (still-empty) cache and each fire their own upstream call,
	// reproducing the very SERVFAIL storm this plugin exists to fix.
	sf singleflight.Group

	// now is overridable in tests.
	now func() time.Time
}

// Name implements plugin.Handler.
func (c *VlanCache) Name() string { return "vlancache" }

// ServeDNS implements plugin.Handler.
func (c *VlanCache) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	now := c.nowFunc()

	ip := net.ParseIP(state.IP())
	qname := state.Name()
	qtype := state.QType()
	do := state.Do()
	cd := r.CheckingDisabled
	ad := r.AuthenticatedData

	scopedKey := hashScoped(qname, qtype, do, cd)
	if e, ok := c.store.getScoped(scopedKey, ip, now); ok {
		attribute(ctx, "cache")
		return c.reply(w, r, e, now, do, ad)
	}

	bucket, ok := c.vlans.Lookup(ip)
	if !ok {
		bucket = globalBucket
	}
	directKey := hashDirect(bucket, qname, qtype, do, cd)
	if e, ok := c.store.getDirect(directKey, now); ok {
		attribute(ctx, "cache")
		return c.reply(w, r, e, now, do, ad)
	}

	bucketKey := strconv.FormatUint(directKey, 36)
	fr, leader, err := c.fetch(ctx, w, bucketKey, directKey, scopedKey, ip, r, now)
	if err != nil {
		return dns.RcodeServerFailure, err
	}
	if !fr.appliesTo(ip) {
		// The bucket-wide leader's answer turned out to be scoped narrower
		// than this requester's address (a per-host upstream policy) —
		// reusing it would leak a different host's answer. Re-fetch,
		// coalesced only with other requesters excluded the same way (same
		// exact IP), instead of forwarding the mismatched answer.
		ipKey := bucketKey + "|" + ip.String()
		fr, leader, err = c.fetch(ctx, w, ipKey, directKey, scopedKey, ip, r, now)
		if err != nil {
			return dns.RcodeServerFailure, err
		}
	}
	// leader is the one caller whose goroutine actually drove Next and hit
	// upstream; every other caller sharing the same fetch got the leader's
	// result without a network call of its own. Distinguishing them here
	// matters: both can take equally long from the client's perspective (a
	// follower waits for the same slow/timing-out upstream call), so
	// querylog's latency-based fallback classification can't tell them
	// apart and would mislabel every coalesced follower as its own
	// "forwarded" — exactly what made a real SERVFAIL storm look unfixed in
	// the query log.
	if leader {
		attribute(ctx, "forward")
	} else {
		attribute(ctx, "coalesced")
	}
	if err := w.WriteMsg(toClientReply(fr.msg, r, do, ad)); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

// attribute self-reports this answer to querylog's in-flight Entry (if any —
// nil-safe when querylog isn't in the chain, e.g. these plugin's own unit
// tests), mirroring plugins/localrecords' own attribute helper.
func attribute(ctx context.Context, verdict string) {
	if e := querylog.FromContext(ctx); e != nil {
		e.Source = "vlancache"
		e.Verdict = verdict
	}
}

// fetchResult is a singleflight-shared upstream response, annotated with
// which requesters it's actually valid for — see fetch and appliesTo.
type fetchResult struct {
	msg *dns.Msg

	// prefix is nil when msg is safe to hand to every requester in the
	// bucket verbatim: either the upstream echoed no RFC 7871 scope (the
	// direct tier's bucket-wide default applies), or the response is a
	// failure (SERVFAIL and friends are IP-independent — see
	// project memory on ECS/cache design). When non-nil, msg is only valid
	// for requesters whose address falls inside prefix.
	prefix *net.IPNet
}

// appliesTo reports whether msg is valid for ip: always true for a
// bucket-wide result, otherwise only when ip falls inside the upstream's
// echoed scope. A nil ip (shouldn't happen for a real client, but guards
// against a panic) is treated as covered rather than forced through a
// second, unkeyable fetch.
func (fr *fetchResult) appliesTo(ip net.IP) bool {
	return fr.prefix == nil || ip == nil || fr.prefix.Contains(ip)
}

// fetch resolves a cache miss, coalescing concurrent callers sharing key
// into a single upstream call via singleflight — see the sf field doc. Only
// the first caller to arrive (the "leader") actually invokes Next; everyone
// else blocks and receives the leader's result share. Sharing across an
// entire VLAN bucket (key == bucketKey, the common case) matches the direct
// tier's own validity assumption (doc.go): one answer is valid for the whole
// bucket by default. But that assumption doesn't hold once the upstream
// actually echoes a host-specific RFC 7871 scope — ServeDNS checks
// fr.appliesTo after this returns and re-fetches under a narrower key for
// any caller the shared result doesn't cover.
//
// captureWriter is used instead of the caller's real w for the upstream
// call: only the leader's goroutine drives Next, but every caller (leader
// included) must still write its own reply, so the leader's write to the
// network happens once at the ServeDNS call site above, not inside fetch.
//
// The returned leader bool reports whether this specific call was the one
// whose closure ran (i.e. actually invoked Next) — singleflight's own
// "shared" return value can't answer that per-caller, since it's the same
// for the leader and every follower whenever there was at least one of each.
func (c *VlanCache) fetch(ctx context.Context, w dns.ResponseWriter, key string, directKey, scopedKey uint64, ip net.IP, r *dns.Msg, now time.Time) (fr *fetchResult, leader bool, err error) {
	v, err, _ := c.sf.Do(key, func() (any, error) {
		leader = true
		cw := &captureWriter{ResponseWriter: w}
		if _, err := plugin.NextOrFailure(c.Name(), c.Next, ctx, cw, r); err != nil {
			return nil, err
		}
		if cw.msg == nil {
			return nil, errNoUpstreamResponse
		}

		res := cw.msg
		mt, _ := response.Typify(res, now)
		fr := &fetchResult{msg: res}
		if cacheable(res, mt) {
			if duration := c.ttlFor(res, mt); duration > 0 {
				e := newEntry(res, now, duration)
				// Failures aren't subnet-dependent, so even an upstream that
				// happens to echo a scope on a SERVFAIL is stored/shared
				// bucket-wide rather than narrowed.
				if prefix := scopePrefix(res, ip); prefix != nil && mt != response.ServerError {
					c.store.setScoped(scopedKey, prefix, e)
					fr.prefix = prefix
				} else {
					c.store.setDirect(directKey, e)
				}
			}
		}
		return fr, nil
	})
	if err != nil {
		return nil, leader, err
	}
	return v.(*fetchResult), leader, nil
}

// toClientReply tailors res (the shared upstream response) into a reply for
// req: fresh Id/Question so it matches this specific caller's request, and
// do/ad mirrored the same way the stock cache plugin and entry.toMsg do
// (RFC 6840 5.7-5.8). RR TTLs are passed through as received — unlike a
// cache-hit reply (entry.toMsg), this response was just fetched, so there is
// no elapsed lifetime to subtract.
func toClientReply(res, req *dns.Msg, do, ad bool) *dns.Msg {
	m := res.Copy()
	m.Id = req.Id
	if len(req.Question) > 0 {
		m.Question = req.Question
	}
	m.Response = true
	if !do && !ad {
		m.AuthenticatedData = false
	}
	return m
}

func (c *VlanCache) reply(w dns.ResponseWriter, r *dns.Msg, e *entry, now time.Time, do, ad bool) (int, error) {
	if err := w.WriteMsg(e.toMsg(r, now, do, ad)); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (c *VlanCache) nowFunc() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now().UTC()
}

// ttlFor decides how long to cache m, mirroring the stock cache plugin's
// per-response-type TTL selection.
func (c *VlanCache) ttlFor(m *dns.Msg, t response.Type) time.Duration {
	switch t {
	case response.NameError, response.NoData:
		msgTTL := dnsutil.MinimalTTLWithMaximum(m, t, maxNTTL)
		return clampDuration(msgTTL, minNTTL, maxNTTL)
	case response.ServerError:
		return c.failTTL
	default:
		msgTTL := dnsutil.MinimalTTLWithMaximum(m, t, maxTTL)
		return clampDuration(msgTTL, minTTL, maxTTL)
	}
}

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// captureWriter records the upstream reply instead of writing it to the
// network. It embeds the leader's real ResponseWriter so downstream plugins
// (e.g. rewrite edns0 subnet) still see the correct RemoteAddr, but the
// actual client write happens once at the ServeDNS call site — inside
// fetch's singleflight closure it would otherwise double-write for the
// leader and never write at all for the coalesced followers.
type captureWriter struct {
	dns.ResponseWriter
	msg *dns.Msg
}

// WriteMsg implements dns.ResponseWriter.
func (w *captureWriter) WriteMsg(res *dns.Msg) error {
	w.msg = res
	return nil
}

// cacheable mirrors the stock cache plugin's key() gate: never cache
// truncated responses, transport/meta/update errors, or an NXDOMAIN with no
// SOA (no way to derive a denial TTL).
func cacheable(m *dns.Msg, t response.Type) bool {
	if m.Truncated {
		return false
	}
	switch t {
	case response.OtherError, response.Meta, response.Update:
		return false
	case response.NameError:
		return hasSOA(m)
	default:
		return true
	}
}

func hasSOA(m *dns.Msg) bool {
	for _, rr := range m.Ns {
		if rr.Header().Rrtype == dns.TypeSOA {
			return true
		}
	}
	return false
}

// scopePrefix reports the RFC 7871 SCOPE-derived network the upstream says
// its response is valid for, if it echoed one. nil means no scope signal —
// the caller falls back to the VLAN-bucket default (doc.go's progressive
// enhancement: honor scope when an upstream provides it, never assume it
// will).
func scopePrefix(m *dns.Msg, ip net.IP) *net.IPNet {
	if ip == nil {
		return nil
	}
	opt := m.IsEdns0()
	if opt == nil {
		return nil
	}
	for _, o := range opt.Option {
		se, ok := o.(*dns.EDNS0_SUBNET)
		if !ok {
			continue
		}
		bits := 32
		if se.Family == 2 {
			bits = 128
		}
		scope := int(se.SourceScope)
		if scope > bits {
			scope = bits
		}
		mask := net.CIDRMask(scope, bits)
		return &net.IPNet{IP: ip.Mask(mask), Mask: mask}
	}
	return nil
}
