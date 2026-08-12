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

	// prefetchThresholdFraction is how much of an entry's original TTL must
	// remain, as a fraction, before a hit triggers a background prefetch
	// refresh — see VlanCache.prefetch's doc comment.
	prefetchThresholdFraction = 0.10
)

// VlanCache is a CoreDNS cache plugin. See doc.go for the design.
type VlanCache struct {
	Next plugin.Handler

	vlans   vlanmatch.Table
	store   *store
	failTTL time.Duration

	// prefetch, when true, proactively refreshes an entry in the background
	// on a hit once its remaining TTL drops below prefetchThresholdFraction
	// of its original TTL — instead of waiting for a client to hit an
	// expired entry and eat the upstream round trip. A deliberately simpler
	// policy than the stock cache plugin's hit-count/duration-gated
	// prefetch (plugin/cache's "prefetch AMOUNT DURATION PERCENTAGE%"): we
	// refresh on every near-expiry hit rather than only after AMOUNT hits
	// within DURATION. Good enough for a homelab's traffic volume, and much
	// simpler than reproducing that windowed counter per cache key.
	prefetch bool
	// staleTTL, when > 0, lets a hit serve an entry up to staleTTL past its
	// original expiry (immediately, with a background refresh kicked off —
	// see refresh) instead of falling through to a synchronous upstream
	// call. 0 disables stale serving entirely (the pre-existing behavior:
	// an expired entry is a miss).
	staleTTL time.Duration

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

	bucket, ok := c.vlans.Lookup(ip)
	if !ok {
		bucket = globalBucket
	}
	scopedKey := hashScoped(qname, qtype, do, cd)
	directKey := hashDirect(bucket, qname, qtype, do, cd)
	bucketKey := strconv.FormatUint(directKey, 36)
	refreshKeys := hitKeys{bucketKey: bucketKey, directKey: directKey, scopedKey: scopedKey, ip: ip, qname: qname, qtype: qtype, do: do, cd: cd}

	if e, fresh, ok := c.store.getScoped(scopedKey, ip, now, c.staleTTL); ok {
		attribute(ctx, "cached")
		c.onHit(ctx, w, e, fresh, now, refreshKeys)
		return c.reply(w, r, e, now, do, ad)
	}

	if e, fresh, ok := c.store.getDirect(directKey, now, c.staleTTL); ok {
		attribute(ctx, "cached")
		c.onHit(ctx, w, e, fresh, now, refreshKeys)
		return c.reply(w, r, e, now, do, ad)
	}

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
	// upstream — that request wasn't served by vlancache, it was a genuine
	// cache miss that fell through to forward, so it must NOT self-attribute
	// here (same fallthrough convention as localrecords/mdnsbridge): leave
	// entry.Source/Verdict blank and let querylog's own latency-based
	// classifyFallback label it "forward"/"forwarded", exactly as it would
	// without vlancache in the chain at all. Only a follower that got the
	// leader's result without a network call of its own is genuinely
	// vlancache's own doing — self-attributed as "coalesced" so it reads as
	// a flavor of vlancache activity (alongside "cached" above), distinct
	// from a real forward, instead of querylog's latency heuristic
	// mislabeling it "forwarded" just because it took just as long as the
	// leader's real upstream call.
	if leader {
		attributeCache(ctx, "miss")
	} else {
		attribute(ctx, "coalesced")
		attributeCache(ctx, "coalesced")
	}
	if err := w.WriteMsg(toClientReply(fr.msg, r, do, ad)); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

// attribute self-reports this answer to querylog's in-flight Entry (if any —
// nil-safe when querylog isn't in the chain, e.g. these plugin's own unit
// tests), mirroring plugins/localrecords' own attribute helper. Only called
// on paths vlancache itself actually served (a hit, or a coalesced follower)
// — a real cache miss that falls through to forward must NOT go through
// this, so it stays attributed to "forward" like it would without vlancache
// in the chain at all (see the ServeDNS call sites).
func attribute(ctx context.Context, verdict string) {
	if e := querylog.FromContext(ctx); e != nil {
		e.Source = "vlancache"
		e.Verdict = verdict
	}
}

// attributeCache records outcome ("hit", "coalesced", "miss") to the
// in-flight Entry's CacheOutcome, unconditionally — unlike attribute, this
// runs on every path including a real miss/forward, so the dashboard can
// show vlancache's full activity breakdown even for the queries that must
// NOT be claimed as Source "vlancache" (see Entry.CacheOutcome's doc
// comment).
func attributeCache(ctx context.Context, outcome string) {
	if e := querylog.FromContext(ctx); e != nil {
		e.CacheOutcome = outcome
	}
}

// hitKeys bundles everything a background refresh (see refresh) needs to
// redo the original lookup/fetch/store cycle for the query that produced a
// cache hit — computed once per ServeDNS call regardless of whether a
// refresh actually ends up firing.
type hitKeys struct {
	bucketKey            string
	directKey, scopedKey uint64
	ip                   net.IP
	qname                string
	qtype                uint16
	do, cd               bool
}

// onHit handles the two ways a store hit can call for background work,
// alongside the synchronous reply ServeDNS always sends from e itself
// (entry.toMsg already clamps a stale entry's TTL to 0 — see its doc
// comment — so the client-visible answer is correct either way):
//
//   - fresh but within prefetchThresholdFraction of expiry: refresh now so
//     the *next* hit doesn't have to pay for a synchronous upstream call.
//   - already past its original TTL (only reachable when c.staleTTL > 0,
//     since store.getDirect/getScoped otherwise wouldn't return ok=true):
//     refresh now, unconditionally — every stale hit needs a refresh.
func (c *VlanCache) onHit(ctx context.Context, w dns.ResponseWriter, e *entry, fresh bool, now time.Time, k hitKeys) {
	if !fresh {
		attributeCache(ctx, "stale")
		c.refresh(w, k)
		return
	}
	attributeCache(ctx, "hit")
	if c.prefetch && nearExpiry(e, now) {
		c.refresh(w, k)
	}
}

// nearExpiry reports whether e has less than prefetchThresholdFraction of
// its original TTL remaining.
func nearExpiry(e *entry, now time.Time) bool {
	if e.origTTL <= 0 {
		return false
	}
	return float64(e.remaining(now))/float64(e.origTTL) <= prefetchThresholdFraction
}

// refresh re-runs the fetch that originally populated this cache entry, in
// the background — used for both prefetch (entry still valid, but close to
// expiry) and stale serving (entry already expired, client got the stale
// answer immediately and shouldn't wait on this). Shares c.sf/c.fetch with
// the synchronous miss path, so a refresh racing a real client miss for the
// same key coalesces into one upstream call rather than two.
//
// Deliberately not attributed to querylog: this isn't a client's query, and
// the triggering client's own *Entry was already (or is about to be)
// recorded by the time this runs — reusing ctx here would risk mutating an
// Entry after querylog's ServeDNS has already read it. Deliberately not
// tied to ctx's lifetime either, for the same reason: this outlives the
// request that triggered it, so a fresh context.Background() is used
// instead — see fetch's use of ctx for what a refresh's upstream call
// actually needs it for (nothing BrambleGate-specific; only forward/rewrite
// read the context, and neither depends on the original request's ctx
// surviving past ServeDNS returning).
//
// A refresh spawned just before an engine.Reload() swaps in a new CoreDNS
// instance can outlive the Next chain it's calling into (e.g. forward's
// upstream connections torn down mid-flight); the error is swallowed like
// any other failed refresh (see fetch's cacheable() gate — a failure just
// leaves the existing entry in place for next time), so this is wasted work
// on reload, not a correctness bug.
func (c *VlanCache) refresh(w dns.ResponseWriter, k hitKeys) {
	go func() {
		req := new(dns.Msg)
		req.SetQuestion(k.qname, k.qtype)
		req.CheckingDisabled = k.cd
		if k.do {
			req.SetEdns0(4096, true)
		}
		_, _, _ = c.fetch(context.Background(), w, k.bucketKey, k.directKey, k.scopedKey, k.ip, req, c.nowFunc())
	}()
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
		_, nextErr := plugin.NextOrFailure(c.Name(), c.Next, ctx, cw, r)

		res := cw.msg
		if res == nil {
			if nextErr == nil {
				return nil, errNoUpstreamResponse
			}
			// Next failed without writing a response — e.g. plugin/forward's
			// real behavior once every upstream connect attempt has
			// timed out: it returns (RcodeServerFailure, err) and leaves the
			// client's SERVFAIL to CoreDNS's own top-level fallback, never
			// producing a *dns.Msg captureWriter can see. That's a genuine
			// upstream failure, not a "no plugin answered" bug, so
			// synthesize the same SERVFAIL the client would otherwise get
			// from that fallback and run it through the normal
			// cacheable()/ttlFor() path below — otherwise this exact
			// failure mode (a connection timeout, as opposed to a DNS-level
			// SERVFAIL reply) would silently bypass caching every time and
			// reproduce the storm this plugin exists to fix.
			res = new(dns.Msg)
			res.SetRcode(r, dns.RcodeServerFailure)
		}

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
