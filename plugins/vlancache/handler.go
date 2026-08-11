package vlancache

import (
	"context"
	"net"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnsutil"
	"github.com/coredns/coredns/plugin/pkg/response"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/vlanmatch"
)

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
		return c.reply(w, r, e, now, do, ad)
	}

	bucket, _ := c.vlans.Lookup(ip)
	directKey := hashDirect(bucket, qname, qtype, do, cd)
	if e, ok := c.store.getDirect(directKey, now); ok {
		return c.reply(w, r, e, now, do, ad)
	}

	cw := &responseWriter{
		ResponseWriter: w,
		cache:          c,
		ip:             ip,
		directKey:      directKey,
		scopedKey:      scopedKey,
		do:             do,
		ad:             ad,
		now:            now,
	}
	return plugin.NextOrFailure(c.Name(), c.Next, ctx, cw, r)
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

// responseWriter intercepts the upstream reply, decides whether/how to
// cache it, and writes it through to the original client unchanged.
type responseWriter struct {
	dns.ResponseWriter
	cache *VlanCache

	ip        net.IP
	directKey uint64
	scopedKey uint64
	do        bool
	ad        bool
	now       time.Time
}

// WriteMsg implements dns.ResponseWriter.
func (w *responseWriter) WriteMsg(res *dns.Msg) error {
	res = res.Copy()
	mt, _ := response.Typify(res, w.now)

	if cacheable(res, mt) {
		if duration := w.cache.ttlFor(res, mt); duration > 0 {
			e := newEntry(res, w.now, duration)
			if prefix := scopePrefix(res, w.ip); prefix != nil {
				w.cache.store.setScoped(w.scopedKey, prefix, e)
			} else {
				w.cache.store.setDirect(w.directKey, e)
			}
		}
	}

	if !w.do && !w.ad {
		res.AuthenticatedData = false
	}
	return w.ResponseWriter.WriteMsg(res)
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
