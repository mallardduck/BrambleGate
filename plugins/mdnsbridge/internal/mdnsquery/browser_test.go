package mdnsquery

import (
	"context"
	"testing"
)

func TestFakeBrowserAdd(t *testing.T) {
	entries := []Entry{
		{
			Host:     "printer.local.",
			Instance: "Office Printer",
			TXT:      map[string]string{"model": "LaserJet"},
			IPv4:     []string{"192.168.1.10"},
		},
		{
			Host:     "nas.local.",
			Instance: "Storage",
			TXT:      map[string]string{"serial": "abc123"},
			IPv4:     []string{"192.168.1.20"},
			IPv6:     []string{"fe80::1"},
		},
	}

	fake := NewFake(entries...)

	var adds []Entry
	err := fake.Browse(context.Background(), "_http._tcp", nil,
		func(e Entry) { adds = append(adds, e) },
		func(e Entry) {}, // not invoked in this test
	)

	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(adds) != len(entries) {
		t.Fatalf("expected %d adds, got %d", len(entries), len(adds))
	}
	for i, got := range adds {
		want := entries[i]
		if got.Host != want.Host || got.Instance != want.Instance {
			t.Errorf("entry[%d] mismatch: got {Host:%s Instance:%s}, want {Host:%s Instance:%s}",
				i, got.Host, got.Instance, want.Host, want.Instance)
		}
		if len(got.IPv4) != len(want.IPv4) || len(got.IPv6) != len(want.IPv6) {
			t.Errorf("entry[%d] IP mismatch: got IPv4:%v IPv6:%v, want IPv4:%v IPv6:%v",
				i, got.IPv4, got.IPv6, want.IPv4, want.IPv6)
		}
	}
}

func TestFakeBrowserContextCanceled(t *testing.T) {
	fake := NewFake(Entry{Host: "test.local."})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := fake.Browse(ctx, "_http._tcp", nil,
		func(e Entry) { t.Fatal("add called after context canceled") },
		func(e Entry) {},
	)

	if err == nil {
		t.Fatal("expected context error")
	}
}
