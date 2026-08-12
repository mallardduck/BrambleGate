package vlancache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// signalNext behaves like stubNext (handler_test.go) but closes a channel
// after its Nth call — async-refresh tests need to wait for a background
// call to actually happen rather than racing a bare counter (stubNext.calls
// has no lock, and prefetch/stale refreshes run concurrently with the test
// goroutine).
type signalNext struct {
	mu    sync.Mutex
	calls int
	fn    func(r *dns.Msg) *dns.Msg
	after int
	done  chan struct{}
}

func newSignalNext(after int, fn func(r *dns.Msg) *dns.Msg) *signalNext {
	return &signalNext{fn: fn, after: after, done: make(chan struct{})}
}

func (s *signalNext) Name() string { return "signal" }

func (s *signalNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	m := s.fn(r)
	_ = w.WriteMsg(m)
	if n == s.after {
		close(s.done)
	}
	return m.Rcode, nil
}

func TestStaleServesImmediatelyAndRefreshesInBackground(t *testing.T) {
	next := newSignalNext(2, func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "1")}
		return m
	})
	vc := build(t, next)
	vc.staleTTL = time.Minute

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA) // populate, ttl=1s

	now = now.Add(5 * time.Second) // expired, but within staleTTL
	m := queryFrom(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if m == nil || len(m.Answer) == 0 {
		t.Fatalf("want an immediate stale answer, got %+v", m)
	}
	if ttl := m.Answer[0].Header().Ttl; ttl != 0 {
		t.Fatalf("stale answer should carry ttl 0 (entry.toMsg clamps remaining to 0), got %d", ttl)
	}

	select {
	case <-next.done:
	case <-time.After(2 * time.Second):
		t.Fatal("want serve-stale to trigger a background refresh (second upstream call)")
	}
}

// TestStaleHitAttributesCacheOutcomeStale checks the CacheOutcome dimension
// specifically: a stale hit is still Source/Verdict "vlancache"/"cached"
// (the client got a cached answer), but CacheOutcome distinguishes it from
// a normal "hit" so the dashboard's cache-activity breakdown can tell served
// -while-refreshing apart from a fully fresh hit.
func TestStaleHitAttributesCacheOutcomeStale(t *testing.T) {
	next := newSignalNext(2, func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "1")}
		return m
	})
	vc := build(t, next)
	vc.staleTTL = time.Minute

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	now = now.Add(5 * time.Second)

	_, e := queryFromWithEntry(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if e.Source != "vlancache" || e.Verdict != "cached" || e.CacheOutcome != "stale" {
		t.Fatalf("Source/Verdict/CacheOutcome = %q/%q/%q, want vlancache/cached/stale", e.Source, e.Verdict, e.CacheOutcome)
	}
	<-next.done // avoid leaking the background refresh goroutine past the test
}

func TestStaleDisabledByDefaultIsSynchronousMiss(t *testing.T) {
	next := &stubNext{fn: func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "1")}
		return m
	}}
	vc := build(t, next) // staleTTL defaults to 0 (disabled)

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	now = now.Add(5 * time.Second) // expired
	queryFrom(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if next.calls != 2 {
		t.Fatalf("want 2 synchronous upstream calls (stale serving disabled means an expired entry is a plain miss), got %d", next.calls)
	}
}

func TestPrefetchRefreshesNearExpiryHit(t *testing.T) {
	next := newSignalNext(2, func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "10")}
		return m
	})
	vc := build(t, next)
	vc.prefetch = true

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA) // populate, ttl=10s
	now = now.Add(9500 * time.Millisecond)                    // 5% of ttl remaining, under the 10% threshold
	m := queryFrom(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if len(m.Answer) == 0 {
		t.Fatal("want the still-valid cached answer served synchronously, not a miss")
	}

	select {
	case <-next.done:
	case <-time.After(2 * time.Second):
		t.Fatal("want prefetch to trigger a background refresh near expiry")
	}
}

func TestPrefetchDisabledByDefaultDoesNotRefresh(t *testing.T) {
	next := newSignalNext(2, func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "10")}
		return m
	})
	vc := build(t, next) // prefetch defaults to false

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	now = now.Add(9500 * time.Millisecond) // well within the 10% prefetch threshold
	queryFrom(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)

	select {
	case <-next.done:
		t.Fatal("prefetch is disabled by default; a near-expiry hit must not trigger a background refresh")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPrefetchDoesNotChangeHitAttribution(t *testing.T) {
	next := newSignalNext(2, func(r *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{aRecord("nas.home.arpa.", "10")}
		return m
	})
	vc := build(t, next)
	vc.prefetch = true

	now := time.Now().UTC()
	vc.now = func() time.Time { return now }

	queryFrom(vc, "192.168.10.5", "nas.home.arpa", dns.TypeA)
	now = now.Add(9500 * time.Millisecond)

	_, e := queryFromWithEntry(vc, "192.168.10.6", "nas.home.arpa", dns.TypeA)
	if e.Source != "vlancache" || e.Verdict != "cached" || e.CacheOutcome != "hit" {
		t.Fatalf("Source/Verdict/CacheOutcome = %q/%q/%q, want vlancache/cached/hit (prefetch is a side effect, not a different outcome)", e.Source, e.Verdict, e.CacheOutcome)
	}
	<-next.done
}
