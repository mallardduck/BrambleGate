package mdnsbridge

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

func mustCIDR(t *testing.T, c string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(c)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGlob(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"_airplay._tcp", "_airplay._tcp", true},
		{"_*._tcp", "_airplay._tcp", true},
		{"living*", "Living Room", true},
		{"*-guest*", "tv-guest-1", true},
		{"synology?", "synology5", true},
		{"synology?", "synology", false},
		{"_http._tcp", "_https._tcp", false},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.p, c.s, got, c.want)
		}
	}
}

func TestSelectorMatch(t *testing.T) {
	vlans := map[string][]*net.IPNet{"trusted": {mustCIDR(t, "192.168.10.0/24")}}
	e := Entry{
		Service:  "_airplay._tcp",
		Instance: "Living Room TV",
		Host:     "livingtv.local.",
		TXT:      map[string]string{"model": "AppleTV6,2"},
		IPv4:     []string{"192.168.10.42"},
	}
	yes := []Selector{
		{}, // zero matches all
		{Service: "_airplay._tcp"},
		{Service: "_*._tcp", Instance: "living*"},
		{Host: "livingtv.local."},
		{TXT: map[string]string{"model": "AppleTV*"}},
		{VLAN: "trusted"},
		{Family: "ipv4"},
	}
	for i, s := range yes {
		if !s.Match(e, vlans) {
			t.Errorf("selector[%d] %+v should match", i, s)
		}
	}
	no := []Selector{
		{Service: "_http._tcp"},
		{Instance: "bedroom*"},
		{TXT: map[string]string{"model": "Roku*"}},
		{VLAN: "guest"}, // undefined vlan
		{Family: "ipv6"},
	}
	for i, s := range no {
		if s.Match(e, vlans) {
			t.Errorf("selector[%d] %+v should NOT match", i, s)
		}
	}
}

func baseCfg() Config {
	return Config{DefaultSuffix: "home.arpa"}
}

func TestUpsertNamingAndAutoPublish(t *testing.T) {
	cfg := baseCfg()
	cfg.AutoPublish = SelectorSet{{Service: "_airplay._tcp"}}
	cfg.Naming = []NamingRule{{Match: Selector{Service: "_airplay._tcp"}, Suffix: "media.home.arpa"}}
	tbl := NewTable(cfg, time.Minute)

	tbl.Upsert(Entry{Host: "AppleTV.local.", Service: "_airplay._tcp", Instance: "Den", IPv4: []string{"10.0.0.5"}})
	tbl.Upsert(Entry{Host: "nas.local.", Service: "_smb._tcp", Instance: "NAS", IPv4: []string{"10.0.0.9"}})

	got := tbl.Snapshot()
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	var appletv, nas Entry
	for _, e := range got {
		switch e.Service {
		case "_airplay._tcp":
			appletv = e
		case "_smb._tcp":
			nas = e
		}
	}
	if appletv.Name != "appletv.media.home.arpa." || !appletv.Published {
		t.Fatalf("airplay entry naming/publish wrong: %+v", appletv)
	}
	if nas.Name != "nas.home.arpa." || nas.Published {
		t.Fatalf("smb entry should use default suffix and not auto-publish: %+v", nas)
	}
}

func TestResolvePromotedPresentAndAbsent(t *testing.T) {
	cfg := baseCfg()
	cfg.Promoted = map[string]Selector{
		"nas.home.arpa.": {Service: "_smb._tcp", Instance: "Synology*"},
	}
	tbl := NewTable(cfg, time.Minute)

	// Absent → owned (NODATA), no addresses.
	if v4, _, owned := tbl.Resolve("nas.home.arpa"); !owned || len(v4) != 0 {
		t.Fatalf("absent promoted name should be owned with no addrs, got owned=%v v4=%v", owned, v4)
	}

	// Present → owned with the matched device's address.
	tbl.Upsert(Entry{Host: "syn.local.", Service: "_smb._tcp", Instance: "Synology NAS", IPv4: []string{"192.168.1.20"}})
	v4, _, owned := tbl.Resolve("nas.home.arpa")
	if !owned || len(v4) != 1 || v4[0] != "192.168.1.20" {
		t.Fatalf("present promoted name should resolve live, got owned=%v v4=%v", owned, v4)
	}

	// A different, non-matching instance must not satisfy the binding.
	if v4, _, _ := tbl.Resolve("other.home.arpa"); len(v4) != 0 {
		t.Fatalf("unbound name should not resolve: %v", v4)
	}
}

func TestManualPublishAndResolveByName(t *testing.T) {
	tbl := NewTable(baseCfg(), time.Minute) // no auto-publish
	tbl.Upsert(Entry{Host: "printer.local.", Service: "_ipp._tcp", Instance: "Office", IPv4: []string{"192.168.1.9"}})

	if _, _, owned := tbl.Resolve("printer.home.arpa"); owned {
		t.Fatal("unpublished candidate should not be owned")
	}
	if !tbl.SetPublished("printer.home.arpa", true) {
		t.Fatal("SetPublished should find the mapped name")
	}
	v4, _, owned := tbl.Resolve("printer.home.arpa")
	if !owned || len(v4) != 1 {
		t.Fatalf("manually published name should resolve, got owned=%v v4=%v", owned, v4)
	}
}

func TestExpire(t *testing.T) {
	now := time.Now()
	tbl := NewTable(baseCfg(), time.Minute)
	tbl.now = func() time.Time { return now }
	tbl.SetConfig(baseCfg()) // rederive with same now
	tbl.Upsert(Entry{Host: "x.local.", Service: "_http._tcp", IPv4: []string{"10.0.0.1"}})
	tbl.SetPublished("x.home.arpa", true)

	now = now.Add(2 * time.Minute)
	if _, _, owned := tbl.Resolve("x.home.arpa"); owned {
		t.Fatal("expired entry should not resolve")
	}
	tbl.Expire()
	if len(tbl.Snapshot()) != 0 {
		t.Fatal("Expire should drop stale entries")
	}
}

// An entry's own real TTL (as propagated from the mDNS record) takes
// priority over the table-wide fallback — including expiring sooner than a
// generous fallback would. This is the point of DefaultTTL being a
// defensive backstop rather than the primary source of truth: a
// short-TTL'd device must still expire on schedule even though the
// fallback is deliberately long.
func TestExpire_UsesEntrysOwnTTLOverFallback(t *testing.T) {
	now := time.Now()
	tbl := NewTable(baseCfg(), time.Hour) // generous fallback
	tbl.now = func() time.Time { return now }
	tbl.Upsert(Entry{Host: "x.local.", Service: "_http._tcp", IPv4: []string{"10.0.0.1"}, TTL: time.Minute})

	now = now.Add(2 * time.Minute) // past the entry's own TTL, well within the fallback
	tbl.Expire()

	if len(tbl.Snapshot()) != 0 {
		t.Fatal("entry with a short real TTL should expire on its own schedule, not the longer fallback")
	}
}

// An entry with no real TTL (unknown) falls back to the table-wide default.
func TestExpire_FallsBackToTableTTLWhenEntryTTLUnknown(t *testing.T) {
	now := time.Now()
	tbl := NewTable(baseCfg(), time.Minute)
	tbl.now = func() time.Time { return now }
	tbl.Upsert(Entry{Host: "x.local.", Service: "_http._tcp", IPv4: []string{"10.0.0.1"}}) // TTL unset

	now = now.Add(30 * time.Second) // within the table's fallback TTL
	tbl.Expire()
	if len(tbl.Snapshot()) != 1 {
		t.Fatal("entry should still be present within the fallback TTL")
	}

	now = now.Add(time.Minute) // now past the fallback TTL
	tbl.Expire()
	if len(tbl.Snapshot()) != 0 {
		t.Fatal("entry with no real TTL should expire per the table's fallback TTL")
	}
}

func TestClear(t *testing.T) {
	tbl := NewTable(baseCfg(), time.Minute)
	tbl.Upsert(Entry{Host: "x.local.", Service: "_http._tcp", Instance: "X", IPv4: []string{"10.0.0.1"}})
	tbl.Upsert(Entry{Host: "y.local.", Service: "_ipp._tcp", Instance: "Y", IPv4: []string{"10.0.0.2"}})

	if dropped := tbl.Clear(); dropped != 2 {
		t.Fatalf("Clear() = %d, want 2", dropped)
	}
	if len(tbl.Snapshot()) != 0 {
		t.Fatal("Clear should drop every entry")
	}

	// A subsequent discovery still works normally after clearing.
	tbl.Upsert(Entry{Host: "z.local.", Service: "_http._tcp", Instance: "Z", IPv4: []string{"10.0.0.3"}})
	if len(tbl.Snapshot()) != 1 {
		t.Fatal("Table should accept new entries after Clear")
	}
}

func bridge(tbl *Table) *MDNSBridge {
	next := plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused // sentinel: fell through
		_ = w.WriteMsg(m)
		return dns.RcodeRefused, nil
	})
	return &MDNSBridge{Next: next, Table: tbl}
}

func query(b *MDNSBridge, name string, qtype uint16) *dns.Msg {
	r := new(dns.Msg)
	r.SetQuestion(dns.Fqdn(name), qtype)
	rec := dnstest.NewRecorder(&test.ResponseWriter{})
	b.ServeDNS(context.Background(), rec, r)
	return rec.Msg
}

func TestServeDNS(t *testing.T) {
	cfg := baseCfg()
	cfg.AutoPublish = SelectorSet{{}} // match-all: auto-publish everything
	cfg.Promoted = map[string]Selector{"nas.home.arpa.": {Service: "_smb._tcp"}}
	tbl := NewTable(cfg, time.Minute)
	tbl.Upsert(Entry{Host: "printer.local.", Service: "_ipp._tcp", IPv4: []string{"192.168.1.9"}})

	// Auto-published name → answered.
	if m := query(bridge(tbl), "printer.home.arpa", dns.TypeA); m.Rcode != dns.RcodeSuccess || len(m.Answer) != 1 {
		t.Fatalf("auto-published name should be answered, rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	// Unknown name → fall through.
	if m := query(bridge(tbl), "unknown.home.arpa", dns.TypeA); m.Rcode != dns.RcodeRefused {
		t.Fatalf("unknown name should fall through, rcode=%d", m.Rcode)
	}
	// Promoted but absent → owned NODATA (NOERROR, no answers), NOT fallthrough.
	if m := query(bridge(tbl), "nas.home.arpa", dns.TypeA); m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("absent promoted name should be NODATA, rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
	// AAAA with only v4 → owned NODATA (name exists, no v6).
	if m := query(bridge(tbl), "printer.home.arpa", dns.TypeAAAA); m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("AAAA with no v6 should be NODATA, rcode=%d answers=%d", m.Rcode, len(m.Answer))
	}
}

func TestSetupRequiresInjectedTable(t *testing.T) {
	SetTable(nil)
	if err := setup(caddy.NewTestController("dns", "mdnsbridge")); err == nil {
		t.Fatal("setup should fail without an injected table")
	}
	SetTable(NewTable(baseCfg(), time.Minute))
	t.Cleanup(func() { SetTable(nil) })
	if err := setup(caddy.NewTestController("dns", "mdnsbridge")); err != nil {
		t.Fatalf("setup should succeed with an injected table: %v", err)
	}
	if err := setup(caddy.NewTestController("dns", "mdnsbridge foo")); err == nil {
		t.Fatal("setup should reject arguments")
	}
}
