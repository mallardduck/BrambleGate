package querylog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLog_LiveSnapshot_NilRing_ReturnsNoEntries(t *testing.T) {
	l := &Log{}
	if got := l.LiveSnapshot(Filter{}); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestLog_LiveSnapshot_ReadsRingOnly(t *testing.T) {
	r := NewRing(16)
	r.Push(Entry{QName: "a.home.arpa."})
	l := &Log{ring: r, stats: newStatsRollup(time.Minute, 10)}

	got := l.LiveSnapshot(Filter{})
	if len(got) != 1 || got[0].QName != "a.home.arpa." {
		t.Fatalf("LiveSnapshot = %+v, want one entry a.home.arpa.", got)
	}
}

func TestLog_Totals_NilStats_ReturnsZeroValue(t *testing.T) {
	l := &Log{}
	got := l.Totals()
	if got.Queries != 0 {
		t.Fatalf("expected a zero-value Totals, got %+v", got)
	}
}

func TestLog_Totals_DelegatesToStats(t *testing.T) {
	stats := newStatsRollup(time.Minute, 10)
	stats.observe(Entry{Verdict: "local"})
	l := &Log{stats: stats}

	if got := l.Totals().Queries; got != 1 {
		t.Fatalf("Totals().Queries = %d, want 1", got)
	}
}

func TestLog_RecentSeries_NilStats_ReturnsNil(t *testing.T) {
	l := &Log{}
	if got := l.RecentSeries(); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestLog_TopDomains_StoreNotConfigured_ReturnsErr(t *testing.T) {
	l := &Log{}
	if _, err := l.TopDomains(context.Background(), time.Hour, 10); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("err = %v, want ErrStoreNotConfigured", err)
	}
}

func TestLog_TopClients_StoreNotConfigured_ReturnsErr(t *testing.T) {
	l := &Log{}
	if _, err := l.TopClients(context.Background(), time.Hour, 10); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("err = %v, want ErrStoreNotConfigured", err)
	}
}

func TestLog_Series_StoreNotConfigured_ReturnsErr(t *testing.T) {
	l := &Log{}
	now := time.Now()
	if _, err := l.Series(context.Background(), now.Add(-time.Hour), now, time.Minute); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("err = %v, want ErrStoreNotConfigured", err)
	}
}

func newTestLog(t *testing.T) *Log {
	t.Helper()
	s := openTestStore(t, StoreConfig{FlushInterval: 10 * time.Millisecond})
	return &Log{store: s}
}

func TestLog_TopDomains_RanksByCount(t *testing.T) {
	l := newTestLog(t)
	now := time.Now()

	l.store.Record(Entry{QName: "a.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "b.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "b.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "c.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "c.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "c.home.arpa.", Timestamp: now})
	waitForRowCount(t, l.store, 6)

	got, err := l.TopDomains(context.Background(), time.Hour, 2)
	if err != nil {
		t.Fatalf("TopDomains: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].QName != "c.home.arpa." || got[0].Count != 3 {
		t.Fatalf("got[0] = %+v, want c.home.arpa.:3", got[0])
	}
	if got[1].QName != "b.home.arpa." || got[1].Count != 2 {
		t.Fatalf("got[1] = %+v, want b.home.arpa.:2", got[1])
	}
}

func TestLog_TopDomains_ExcludesOutsideWindow(t *testing.T) {
	l := newTestLog(t)
	now := time.Now()

	l.store.Record(Entry{QName: "recent.home.arpa.", Timestamp: now})
	l.store.Record(Entry{QName: "old.home.arpa.", Timestamp: now.Add(-2 * time.Hour)})
	waitForRowCount(t, l.store, 2)

	got, err := l.TopDomains(context.Background(), time.Hour, 10)
	if err != nil {
		t.Fatalf("TopDomains: %v", err)
	}
	if len(got) != 1 || got[0].QName != "recent.home.arpa." {
		t.Fatalf("got = %+v, want only recent.home.arpa.", got)
	}
}

func TestLog_TopClients_GroupsByIPAndVLAN(t *testing.T) {
	l := newTestLog(t)
	now := time.Now()

	l.store.Record(Entry{Client: ClientInfo{IP: "10.0.0.5", VLAN: "trusted"}, Timestamp: now})
	l.store.Record(Entry{Client: ClientInfo{IP: "10.0.0.5", VLAN: "trusted"}, Timestamp: now})
	// Same IP, different VLAN — must be tracked as a distinct row, not
	// merged with the trusted-VLAN entries above (dev-docs/query-log.md's
	// VLAN-awareness differentiator).
	l.store.Record(Entry{Client: ClientInfo{IP: "10.0.0.5", VLAN: "iot"}, Timestamp: now})
	waitForRowCount(t, l.store, 3)

	got, err := l.TopClients(context.Background(), time.Hour, 10)
	if err != nil {
		t.Fatalf("TopClients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 distinct (ip,vlan) rows, got %+v", len(got), got)
	}
	if got[0].ClientIP != "10.0.0.5" || got[0].VLAN != "trusted" || got[0].Count != 2 {
		t.Fatalf("got[0] = %+v, want 10.0.0.5/trusted:2", got[0])
	}
}

func TestLog_Series_DenseZeroFilledBuckets(t *testing.T) {
	l := newTestLog(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	bucket := time.Minute

	// Only the 2nd and 4th minute buckets get any queries; the rest of the
	// [from,to) range must still come back as zero-count points, not be
	// omitted, so a caller can chart it directly.
	l.store.Record(Entry{Timestamp: from.Add(90 * time.Second)})               // minute bucket 1
	l.store.Record(Entry{Timestamp: from.Add(3*time.Minute + 30*time.Second)}) // bucket 3
	l.store.Record(Entry{Timestamp: from.Add(3*time.Minute + 40*time.Second)}) // bucket 3
	waitForRowCount(t, l.store, 3)

	got, err := l.Series(context.Background(), from, to, bucket)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (dense over [from,to) at 1-min buckets)", len(got))
	}
	want := []int64{0, 1, 0, 2, 0}
	for i, b := range got {
		if b.Count != want[i] {
			t.Fatalf("bucket %d count = %d, want %d (full series: %+v)", i, b.Count, want[i], got)
		}
		wantStart := from.Add(time.Duration(i) * bucket)
		if !b.Start.Equal(wantStart) {
			t.Fatalf("bucket %d start = %v, want %v", i, b.Start, wantStart)
		}
	}
}

func TestLog_Series_ToNotAfterFrom_Errors(t *testing.T) {
	l := newTestLog(t)
	now := time.Now()
	if _, err := l.Series(context.Background(), now, now, time.Minute); err == nil {
		t.Fatal("expected an error when to == from")
	}
}

func TestCurrentLog_ComposesProcessWideSingletons(t *testing.T) {
	t.Cleanup(func() { SetCurrent(nil) })
	r := NewRing(4)
	SetCurrent(r)

	l := CurrentLog()
	if l.ring != r {
		t.Fatal("expected CurrentLog().ring to be the process-wide Ring")
	}
	if l.stats != globalStats {
		t.Fatal("expected CurrentLog().stats to be the shared globalStats singleton")
	}
}
