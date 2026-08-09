package mdnssd

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const testQuestion = "_http._tcp.local."
const testInstance = "Foo._http._tcp.local."

func ptrMsg(question, target string, ttl time.Duration) *dns.Msg {
	m := new(dns.Msg)
	m.Answer = []dns.RR{&dns.PTR{Hdr: rrHeader(question, dns.TypePTR, uint32(ttl.Seconds())), Ptr: target}}
	return m
}

func srvMsg(instance, target string, port int, ttl time.Duration) *dns.Msg {
	m := new(dns.Msg)
	m.Answer = []dns.RR{&dns.SRV{Hdr: rrHeader(instance, dns.TypeSRV, uint32(ttl.Seconds())), Target: target, Port: uint16(port)}}
	return m
}

func aMsg(host string, ip net.IP, ttl time.Duration) *dns.Msg {
	m := new(dns.Msg)
	m.Answer = []dns.RR{&dns.A{Hdr: rrHeader(host, dns.TypeA, uint32(ttl.Seconds())), A: ip}}
	return m
}

// --- browserState: pure, synchronous core -------------------------------

func TestBrowserState_InitialQuery_SetsQUBit(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())
	msg := state.initialQuery()

	if msg.Question[0].Name != testQuestion {
		t.Errorf("Name = %q, want %q", msg.Question[0].Name, testQuestion)
	}
	if msg.Question[0].Qclass&quBit == 0 {
		t.Error("QU bit not set on initial query")
	}
}

func TestBrowserState_Ingest_NoAddUntilResolvable(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())

	added, _ := state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", time.Now())

	if len(added) != 0 {
		t.Errorf("added = %+v, want none — no host/address known yet", added)
	}
}

func TestBrowserState_Ingest_FiresAddOnceHostAndAddressKnown(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())
	now := time.Now()

	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", now)
	state.Ingest(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0", now)
	added, _ := state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0", now)

	if len(added) != 1 {
		t.Fatalf("added = %+v, want 1 entry", added)
	}
	e := added[0]
	if e.Host != "foo.local" || e.Instance != "Foo" || e.Type != "_http._tcp" || e.Domain != "local" {
		t.Errorf("entry = %+v, unexpected fields", e)
	}
	if len(e.IPv4) != 1 || e.IPv4[0] != "192.168.1.5" {
		t.Errorf("IPv4 = %v, want [192.168.1.5]", e.IPv4)
	}
}

func TestBrowserState_Ingest_DoesNotReAddUnchangedEntry(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())
	now := time.Now()
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", now)
	state.Ingest(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0", now)
	state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0", now)

	// A periodic re-announcement of the exact same PTR shouldn't re-fire add.
	added, _ := state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", now)

	if len(added) != 0 {
		t.Errorf("added = %+v, want none — nothing changed", added)
	}
}

func TestBrowserState_Ingest_ReAddsWhenNewAddressArrives(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())
	now := time.Now()
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", now)
	state.Ingest(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0", now)
	state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0", now)

	added, _ := state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.6"), 100*time.Second), "eth0", now)

	if len(added) != 1 {
		t.Fatalf("added = %+v, want 1 (updated) entry", added)
	}
	if len(added[0].IPv4) != 2 {
		t.Errorf("IPv4 = %v, want both addresses merged", added[0].IPv4)
	}
}

func TestBrowserState_Ingest_GoodbyePacketRemovesResolvedEntry(t *testing.T) {
	state := newBrowserState(testQuestion, newFakeClock())
	now := time.Now()
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", now)
	state.Ingest(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0", now)
	state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0", now)

	_, removed := state.Ingest(ptrMsg(testQuestion, testInstance, 0), "eth0", now) // TTL=0: goodbye

	if len(removed) != 1 || removed[0].Host != "foo.local" {
		t.Errorf("removed = %+v, want the resolved entry removed", removed)
	}
}

func TestBrowserState_Tick_RequeriesAtRefreshThreshold(t *testing.T) {
	clock := newFakeClock()
	state := newBrowserState(testQuestion, clock)
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", clock.Now())

	clock.Advance(80 * time.Second)
	toQuery, removed := state.Tick(clock.Now())

	if len(toQuery) != 1 || toQuery[0].Question[0].Name != testQuestion {
		t.Errorf("toQuery = %+v, want one re-query for %q", toQuery, testQuestion)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v, want none — still within TTL", removed)
	}
}

// The dnssd #63 regression, at the browser level: a device that goes quiet
// (no refresh answer) must be removed once its TTL fully elapses, not kept
// forever, and not removed before then either.
func TestBrowserState_Tick_RemovesAfterTTLWithNoRefreshAnswer(t *testing.T) {
	clock := newFakeClock()
	state := newBrowserState(testQuestion, clock)
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", clock.Now())
	state.Ingest(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0", clock.Now())
	state.Ingest(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0", clock.Now())

	clock.Advance(95 * time.Second)
	_, removed := state.Tick(clock.Now())
	if len(removed) != 0 {
		t.Fatalf("removed = %+v, want none yet (95%% of TTL)", removed)
	}

	clock.Advance(5 * time.Second) // TTL fully elapsed, no re-announcement seen
	_, removed = state.Tick(clock.Now())
	if len(removed) != 1 || removed[0].Host != "foo.local" {
		t.Errorf("removed = %+v, want the entry evicted", removed)
	}
}

// A device that re-announces before its refresh threshold's answer would
// have mattered must NOT be spuriously removed — the whole point of active
// refresh over dnssd's passive-only behavior.
func TestBrowserState_ReAnnounceBeforeExpiry_NoSpuriousRemove(t *testing.T) {
	clock := newFakeClock()
	state := newBrowserState(testQuestion, clock)
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", clock.Now())

	clock.Advance(80 * time.Second)
	state.Tick(clock.Now()) // refresh query goes out

	clock.Advance(5 * time.Second) // t=85s: device re-announces
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", clock.Now())

	clock.Advance(79 * time.Second) // t=164s: past the original t=100s expiry
	_, removed := state.Tick(clock.Now())

	if len(removed) != 0 {
		t.Errorf("removed = %+v, want none — the record was refreshed", removed)
	}
}

// A refresh re-query should offer other still-fresh records answering the
// same question as known answers (RFC 6762 §7.1), so a responder can skip
// re-sending them. The record that actually triggered the refresh does NOT
// qualify (by the time it's 80%+ through its TTL — the earliest a refresh
// fires — it has under 50% TTL remaining, below §7.1's known-answer bar) —
// this test exercises a second, freshly-seen instance instead.
func TestBrowserState_Tick_IncludesKnownAnswerInRequery(t *testing.T) {
	const otherInstance = "Bar._http._tcp.local."
	clock := newFakeClock()
	state := newBrowserState(testQuestion, clock)
	state.Ingest(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0", clock.Now())

	clock.Advance(80 * time.Second) // testInstance now needs refreshing...
	// ...but otherInstance was just seen, well within its own fresh TTL.
	state.Ingest(ptrMsg(testQuestion, otherInstance, 100*time.Second), "eth0", clock.Now())

	toQuery, _ := state.Tick(clock.Now())

	if len(toQuery) != 1 || len(toQuery[0].Answer) != 1 {
		t.Fatalf("toQuery = %+v, want 1 message with 1 known answer", toQuery)
	}
	ptr, ok := toQuery[0].Answer[0].(*dns.PTR)
	if !ok || ptr.Ptr != otherInstance {
		t.Errorf("known answer = %+v, want the fresh record (%q), not the one being refreshed", toQuery[0].Answer[0], otherInstance)
	}
}

// --- Browser.Browse: thin async wiring over a fake Transport -------------

func TestBrowse_SendsInitialQueryOnStart(t *testing.T) {
	transport := newFakeTransport()
	b := New(WithTransport(transport), WithClock(newFakeClock()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.Browse(ctx, "_http._tcp", nil, func(Entry) {}, func(Entry) {}) }()

	deadline := time.Now().Add(2 * time.Second)
	for transport.sentCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	sent := transport.sentMessages()
	if len(sent) != 1 || sent[0].Question[0].Name != testQuestion {
		t.Fatalf("sent = %+v, want one initial query for %q", sent, testQuestion)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Browse did not return after context cancellation")
	}
}

func TestBrowse_FiresAddCallbackOnResolvedInstance(t *testing.T) {
	transport := newFakeTransport()
	b := New(WithTransport(transport), WithClock(newFakeClock()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	added := make(chan Entry, 4)
	done := make(chan error, 1)
	go func() {
		done <- b.Browse(ctx, "_http._tcp", nil, func(e Entry) { added <- e }, func(Entry) {})
	}()

	transport.deliver(ptrMsg(testQuestion, testInstance, 100*time.Second), "eth0")
	transport.deliver(srvMsg(testInstance, "foo.local.", 8080, 100*time.Second), "eth0")
	transport.deliver(aMsg("foo.local.", net.ParseIP("192.168.1.5"), 100*time.Second), "eth0")

	select {
	case e := <-added:
		if e.Host != "foo.local" || e.Instance != "Foo" {
			t.Errorf("added entry = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for add callback")
	}

	cancel()
	<-done
}

func TestBrowse_ReturnsErrorWithoutTransport(t *testing.T) {
	b := New()
	err := b.Browse(context.Background(), "_http._tcp", nil, func(Entry) {}, func(Entry) {})
	if err == nil {
		t.Error("err = nil, want an error when no Transport is configured")
	}
}
