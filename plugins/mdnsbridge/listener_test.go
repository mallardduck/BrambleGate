package mdnsbridge

import (
	"context"
	"log/slog"
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
