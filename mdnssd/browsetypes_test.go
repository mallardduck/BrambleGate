package mdnssd

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const metaQuestion = "_services._dns-sd._udp.local."

func TestTypeBrowserState_InitialQuery_TargetsMetaService(t *testing.T) {
	state := newTypeBrowserState(newFakeClock())
	msg := state.initialQuery()

	if msg.Question[0].Name != metaQuestion {
		t.Errorf("Name = %q, want %q", msg.Question[0].Name, metaQuestion)
	}
	if msg.Question[0].Qclass&quBit == 0 {
		t.Error("QU bit not set on initial query")
	}
}

func TestTypeBrowserState_Ingest_EmitsDiscoveredType(t *testing.T) {
	state := newTypeBrowserState(newFakeClock())

	got := state.Ingest(ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second), time.Now())

	if len(got) != 1 || got[0] != "_http._tcp" {
		t.Errorf("got = %v, want [\"_http._tcp\"]", got)
	}
}

func TestTypeBrowserState_Ingest_DedupesRepeatedAnnouncements(t *testing.T) {
	state := newTypeBrowserState(newFakeClock())
	now := time.Now()
	state.Ingest(ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second), now)

	got := state.Ingest(ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second), now)

	if len(got) != 0 {
		t.Errorf("got = %v, want none — type already reported", got)
	}
}

func TestTypeBrowserState_Ingest_MultipleTypesInOneMessage(t *testing.T) {
	state := newTypeBrowserState(newFakeClock())
	msg := ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second)
	msg.Answer = append(msg.Answer, &dns.PTR{
		Hdr: rrHeader(metaQuestion, dns.TypePTR, 100),
		Ptr: "_ipp._tcp.local.",
	})

	got := state.Ingest(msg, time.Now())

	if len(got) != 2 {
		t.Fatalf("got = %v, want 2 types", got)
	}
}

func TestTypeBrowserState_Ingest_IgnoresUnrelatedQuestions(t *testing.T) {
	state := newTypeBrowserState(newFakeClock())

	got := state.Ingest(ptrMsg("_http._tcp.local.", "Foo._http._tcp.local.", 100*time.Second), time.Now())

	if len(got) != 0 {
		t.Errorf("got = %v, want none — not the meta-query question", got)
	}
}

func TestTypeBrowserState_Tick_RequeriesAtRefreshThreshold(t *testing.T) {
	clock := newFakeClock()
	state := newTypeBrowserState(clock)
	state.Ingest(ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second), clock.Now())

	clock.Advance(80 * time.Second)
	toQuery := state.Tick(clock.Now())

	if len(toQuery) != 1 || toQuery[0].Question[0].Name != metaQuestion {
		t.Errorf("toQuery = %+v, want one re-query for %q", toQuery, metaQuestion)
	}
}

func TestBrowseTypes_SendsInitialMetaQuery(t *testing.T) {
	transport := newFakeTransport()
	b := New(WithTransport(transport), WithClock(newFakeClock()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.BrowseTypes(ctx, nil, func(string) {}) }()

	deadline := time.Now().Add(2 * time.Second)
	for transport.sentCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	sent := transport.sentMessages()
	if len(sent) != 1 || sent[0].Question[0].Name != metaQuestion {
		t.Fatalf("sent = %+v, want one initial meta-query", sent)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BrowseTypes did not return after context cancellation")
	}
}

func TestBrowseTypes_FiresOnTypeCallback(t *testing.T) {
	transport := newFakeTransport()
	b := New(WithTransport(transport), WithClock(newFakeClock()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	types := make(chan string, 4)
	done := make(chan error, 1)
	go func() { done <- b.BrowseTypes(ctx, nil, func(typ string) { types <- typ }) }()

	transport.deliver(ptrMsg(metaQuestion, "_http._tcp.local.", 100*time.Second), "eth0")

	select {
	case typ := <-types:
		if typ != "_http._tcp" {
			t.Errorf("typ = %q, want \"_http._tcp\"", typ)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onType callback")
	}

	cancel()
	<-done
}
