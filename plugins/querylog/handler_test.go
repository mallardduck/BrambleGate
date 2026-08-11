package querylog

import (
	"context"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	coretest "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"

	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// fakeClock returns times sequentially from ts on each call, repeating the
// last entry once exhausted — used to make ServeDNS's measured Latency (and
// therefore its cache/forward fallback classification) deterministic instead
// of depending on real wall-clock timing during the test.
func fakeClock(ts ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := ts[i]
		if i < len(ts)-1 {
			i++
		}
		return t
	}
}

func newTestQueryLog(next plugin.Handler, now func() time.Time) *QueryLog {
	return &QueryLog{
		Next: next,
		Ring: NewRing(16),
		VLANs: vlanmatch.NewTable([]vlanmatch.VLAN{
			{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
		}),
		Now: now,
	}
}

func localAnswerHandler() plugin.Handler {
	return plugin.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		if e := FromContext(ctx); e != nil {
			e.Source = "localrecords"
			e.Verdict = "local"
		}
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{coretest.A("nas.home.arpa. 300 IN A 192.168.10.20")}
		_ = w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	})
}

func nxdomainHandler() plugin.Handler {
	return plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNameError)
		_ = w.WriteMsg(m)
		return dns.RcodeNameError, nil
	})
}

func noopHandler() plugin.Handler {
	return plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		_ = w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	})
}

func cnameAnswerHandler() plugin.Handler {
	return plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.AuthenticatedData = true
		m.Answer = []dns.RR{coretest.CNAME("git.home.arpa. 300 IN CNAME nas.home.arpa.")}
		_ = w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	})
}

func TestServeDNS_SelfAttributedAnswer_KeepsDownstreamSourceVerdict(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(localAnswerHandler(), fakeClock(start, start.Add(time.Millisecond)))

	w := &coretest.ResponseWriter{RemoteIP: "192.168.10.5"}
	r := new(dns.Msg)
	r.SetQuestion("nas.home.arpa.", dns.TypeA)

	if _, err := q.ServeDNS(context.Background(), w, r); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})
	if len(got) != 1 {
		t.Fatalf("Ring has %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Source != "localrecords" || e.Verdict != "local" {
		t.Errorf("Source/Verdict = %q/%q, want localrecords/local (self-attributed, querylog must not override)", e.Source, e.Verdict)
	}
	if e.QName != "nas.home.arpa." || e.QType != dns.TypeA {
		t.Errorf("QName/QType = %q/%d, want nas.home.arpa./%d", e.QName, e.QType, dns.TypeA)
	}
	if e.Client.IP != "192.168.10.5" || e.Client.VLAN != "trusted" {
		t.Errorf("Client = %+v, want IP=192.168.10.5 VLAN=trusted", e.Client)
	}
	if e.Rcode != dns.RcodeSuccess {
		t.Errorf("Rcode = %d, want %d", e.Rcode, dns.RcodeSuccess)
	}
	if e.Latency != time.Millisecond {
		t.Errorf("Latency = %v, want 1ms", e.Latency)
	}
}

func TestServeDNS_CapturesListenerAndProto(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(noopHandler(), fakeClock(start, start))

	if _, err := q.ServeDNS(context.Background(), &coretest.ResponseWriter{}, mustQuestion("a.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})[0]
	if got.Listener != "127.0.0.1:53" || got.Proto != "udp" {
		t.Errorf("Listener/Proto = %q/%q, want 127.0.0.1:53/udp", got.Listener, got.Proto)
	}
}

func TestServeDNS_CapturesListenerAndProto_TCP(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(noopHandler(), fakeClock(start, start))

	w := &coretest.ResponseWriter{TCP: true}
	if _, err := q.ServeDNS(context.Background(), w, mustQuestion("a.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})[0]
	if got.Proto != "tcp" {
		t.Errorf("Proto = %q, want tcp", got.Proto)
	}
}

func TestServeDNS_CapturesAuthenticatedDataAndAnswerType(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(cnameAnswerHandler(), fakeClock(start, start))

	if _, err := q.ServeDNS(context.Background(), &coretest.ResponseWriter{}, mustQuestion("git.home.arpa.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})[0]
	if !got.AuthenticatedData {
		t.Error("AuthenticatedData = false, want true (handler set AD on the reply)")
	}
	if got.AnswerType != "CNAME" {
		t.Errorf("AnswerType = %q, want CNAME", got.AnswerType)
	}
}

func TestServeDNS_NXDOMAIN_AnswerTypeEmpty(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(nxdomainHandler(), fakeClock(start, start))

	if _, err := q.ServeDNS(context.Background(), &coretest.ResponseWriter{}, mustQuestion("nope.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})[0]
	if got.AnswerType != "" {
		t.Errorf("AnswerType = %q, want empty (no answer records)", got.AnswerType)
	}
	if got.AuthenticatedData {
		t.Error("AuthenticatedData = true, want false (handler did not set AD)")
	}
}

func TestServeDNS_NXDOMAIN_FallbackVerdict(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(nxdomainHandler(), fakeClock(start, start))

	w := &coretest.ResponseWriter{}
	r := new(dns.Msg)
	r.SetQuestion("nope.example.com.", dns.TypeA)

	if _, err := q.ServeDNS(context.Background(), w, r); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})
	if len(got) != 1 {
		t.Fatalf("Ring has %d entries, want 1", len(got))
	}
	if got[0].Verdict != "nxdomain" {
		t.Errorf("Verdict = %q, want nxdomain", got[0].Verdict)
	}
	if got[0].Rcode != dns.RcodeNameError {
		t.Errorf("Rcode = %d, want %d", got[0].Rcode, dns.RcodeNameError)
	}
}

func TestServeDNS_FallbackClassification_FastIsCache_SlowIsForward(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	fast := newTestQueryLog(noopHandler(), fakeClock(start, start))
	if _, err := fast.ServeDNS(context.Background(), &coretest.ResponseWriter{}, mustQuestion("a.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}
	fastEntry := fast.Ring.Snapshot(Filter{})[0]
	if fastEntry.Source != "cache" || fastEntry.Verdict != "cached" {
		t.Errorf("fast fallback Source/Verdict = %q/%q, want cache/cached", fastEntry.Source, fastEntry.Verdict)
	}

	slow := newTestQueryLog(noopHandler(), fakeClock(start, start.Add(50*time.Millisecond)))
	if _, err := slow.ServeDNS(context.Background(), &coretest.ResponseWriter{}, mustQuestion("b.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}
	slowEntry := slow.Ring.Snapshot(Filter{})[0]
	if slowEntry.Source != "forward" || slowEntry.Verdict != "forwarded" {
		t.Errorf("slow fallback Source/Verdict = %q/%q, want forward/forwarded", slowEntry.Source, slowEntry.Verdict)
	}
}

func mustQuestion(qname string) *dns.Msg {
	r := new(dns.Msg)
	r.SetQuestion(qname, dns.TypeA)
	return r
}

func TestServeDNS_NoVLANMatch_ClientVLANEmpty(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueryLog(noopHandler(), fakeClock(start, start))

	w := &coretest.ResponseWriter{RemoteIP: "203.0.113.5"}
	if _, err := q.ServeDNS(context.Background(), w, mustQuestion("a.example.com.")); err != nil {
		t.Fatalf("ServeDNS: %v", err)
	}

	got := q.Ring.Snapshot(Filter{})[0]
	if got.Client.VLAN != "" {
		t.Errorf("Client.VLAN = %q, want empty (no VLAN matches 203.0.113.5)", got.Client.VLAN)
	}
}
