package mdnsbridge

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge/internal/mdnsquery"
)

func TestListenerIngestsAndRemoves(t *testing.T) {
	// Mock browser with canned entries
	fakeEntry := mdnsquery.Entry{
		Host:     "printer.local.",
		Instance: "Office Printer",
		TXT:      map[string]string{"model": "LaserJet"},
		IPv4:     []string{"192.168.1.10"},
		IPv6:     []string{"fe80::10"},
	}
	fake := mdnsquery.NewFake(fakeEntry)

	// Create a Listener with auto-publish matching everything
	cfg := Config{DefaultSuffix: "home.arpa", AutoPublish: SelectorSet{{}}}
	table := NewTable(cfg, time.Minute)
	listener := &Listener{
		table:    table,
		services: []string{"_http._tcp"},
		browser:  fake,
		log:      slog.New(slog.DiscardHandler),
	}

	// Run a browse cycle
	ctx, cancel := context.WithCancel(context.Background())
	go listener.browseService(ctx, "_http._tcp")
	// Let the browse complete immediately since fake is synchronous
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-time.After(10 * time.Millisecond) // let goroutine finish

	// Verify the entry was ingested and published
	snapshot := table.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snapshot))
	}
	e := snapshot[0]
	if !e.Published {
		t.Errorf("entry should be auto-published")
	}
	if e.Service != "_http._tcp" {
		t.Errorf("service mismatch: got %s, want _http._tcp", e.Service)
	}
	if e.Instance != "Office Printer" {
		t.Errorf("instance mismatch: got %s, want Office Printer", e.Instance)
	}
	if len(e.IPv4) != 1 || e.IPv4[0] != "192.168.1.10" {
		t.Errorf("IPv4 mismatch: got %v, want [192.168.1.10]", e.IPv4)
	}
	if len(e.IPv6) != 1 || e.IPv6[0] != "fe80::10" {
		t.Errorf("IPv6 mismatch: got %v, want [fe80::10]", e.IPv6)
	}
}

func TestListenerRemovesEntry(t *testing.T) {
	cfg := Config{DefaultSuffix: "home.arpa", AutoPublish: SelectorSet{{}}}
	table := NewTable(cfg, time.Minute)

	// Manually insert an entry
	table.Upsert(Entry{
		Host:     "test.local",
		Service:  "_http._tcp",
		Instance: "Test",
		IPv4:     []string{"192.168.1.1"},
	})

	snapshot := table.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("setup: expected 1 entry, got %d", len(snapshot))
	}

	// Test Remove
	listener := &Listener{table: table, log: slog.New(slog.DiscardHandler)}
	e := mdnsquery.Entry{
		Host:     "test.local",
		Instance: "Test",
	}
	listener.remove("_http._tcp", e)

	// Verify it was removed
	snapshot = table.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("entry should be removed, but got %d entries", len(snapshot))
	}
}

func TestUsesDynamicServices(t *testing.T) {
	cases := []struct {
		name     string
		services []string
		want     bool
	}{
		{"empty", nil, false},
		{"explicit list", []string{"_http._tcp"}, false},
		{"all lowercase", []string{"all"}, true},
		{"all uppercase", []string{"ALL"}, true},
		{"all plus another entry", []string{"all", "_http._tcp"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &Listener{services: c.services}
			if got := l.usesDynamicServices(); got != c.want {
				t.Errorf("usesDynamicServices() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestListenerBrowseAllTypes_DiscoversAndIngestsEntry(t *testing.T) {
	fakeEntry := mdnsquery.Entry{
		Host:     "printer.local.",
		Instance: "Office Printer",
		IPv4:     []string{"192.168.1.10"},
	}
	fake := mdnsquery.NewFake(fakeEntry).WithTypes("_http._tcp")

	cfg := Config{DefaultSuffix: "home.arpa", AutoPublish: SelectorSet{{}}}
	table := NewTable(cfg, time.Minute)
	listener := &Listener{
		table:    table,
		services: []string{"all"},
		browser:  fake,
		log:      slog.New(slog.DiscardHandler),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go listener.browseAllTypes(ctx)
	time.Sleep(20 * time.Millisecond) // let discovery + the spawned (synchronous, fake) browseService complete
	cancel()
	time.Sleep(10 * time.Millisecond)

	snapshot := table.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 entry discovered via dynamic type browsing, got %d", len(snapshot))
	}
	if snapshot[0].Service != "_http._tcp" {
		t.Errorf("service = %q, want _http._tcp", snapshot[0].Service)
	}
}

// countingBrowser is a Browser test double that records how many times
// Browse is called per service type, so dedup logic can be asserted
// directly rather than inferred from downstream Table state (which would
// merge duplicate ingests into one entry regardless of dedup).
type countingBrowser struct {
	mu          sync.Mutex
	browseCalls map[string]int
	types       []string
}

func (c *countingBrowser) Browse(ctx context.Context, service string, ifaceNames []string, add mdnsquery.AddFunc, rmv mdnsquery.RmvFunc) error {
	c.mu.Lock()
	c.browseCalls[service]++
	c.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (c *countingBrowser) BrowseTypes(ctx context.Context, ifaceNames []string, onType mdnsquery.TypeFunc) error {
	for _, t := range c.types {
		onType(t)
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestListenerBrowseAllTypes_DedupsRepeatedTypeAnnouncements(t *testing.T) {
	cb := &countingBrowser{browseCalls: map[string]int{}, types: []string{"_http._tcp", "_http._tcp", "_ipp._tcp"}}
	table := NewTable(Config{DefaultSuffix: "home.arpa"}, time.Minute)
	listener := &Listener{table: table, services: []string{"all"}, browser: cb, log: slog.New(slog.DiscardHandler)}

	ctx, cancel := context.WithCancel(context.Background())
	go listener.browseAllTypes(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.browseCalls["_http._tcp"] != 1 {
		t.Errorf("_http._tcp Browse called %d times, want 1 (dedup)", cb.browseCalls["_http._tcp"])
	}
	if cb.browseCalls["_ipp._tcp"] != 1 {
		t.Errorf("_ipp._tcp Browse called %d times, want 1", cb.browseCalls["_ipp._tcp"])
	}
}
