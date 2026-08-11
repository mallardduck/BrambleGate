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

func TestLog_ClientActivity_StoreNotConfigured_ReturnsErr(t *testing.T) {
	l := &Log{}
	now := time.Now()
	if _, err := l.ClientActivity(context.Background(), now.Add(-time.Hour), now, time.Minute, 5); !errors.Is(err, ErrStoreNotConfigured) {
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

func TestLog_ClientActivity_RanksAndFoldsOthers(t *testing.T) {
	l := newTestLog(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)
	bucket := time.Minute

	// a: 3 queries (top), b: 2 queries (top), c: 1 query (folded into
	// Other, since topN below is 2).
	l.store.Record(Entry{Client: ClientInfo{IP: "a"}, Timestamp: from.Add(10 * time.Second)})
	l.store.Record(Entry{Client: ClientInfo{IP: "a"}, Timestamp: from.Add(20 * time.Second)})
	l.store.Record(Entry{Client: ClientInfo{IP: "a"}, Timestamp: from.Add(70 * time.Second)})
	l.store.Record(Entry{Client: ClientInfo{IP: "b"}, Timestamp: from.Add(10 * time.Second)})
	l.store.Record(Entry{Client: ClientInfo{IP: "b"}, Timestamp: from.Add(15 * time.Second)})
	l.store.Record(Entry{Client: ClientInfo{IP: "c"}, Timestamp: from.Add(10 * time.Second)})
	waitForRowCount(t, l.store, 6)

	got, err := l.ClientActivity(context.Background(), from, to, bucket, 2)
	if err != nil {
		t.Fatalf("ClientActivity: %v", err)
	}
	if len(got.Buckets) != 2 {
		t.Fatalf("len(Buckets) = %d, want 2", len(got.Buckets))
	}
	if len(got.Rows) != 3 {
		t.Fatalf("len(Rows) = %d, want 3 (a, b, Other), got %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0].ClientIP != "a" || got.Rows[0].Counts[0] != 2 || got.Rows[0].Counts[1] != 1 {
		t.Fatalf("Rows[0] = %+v, want a with counts [2 1]", got.Rows[0])
	}
	if got.Rows[1].ClientIP != "b" || got.Rows[1].Counts[0] != 2 || got.Rows[1].Counts[1] != 0 {
		t.Fatalf("Rows[1] = %+v, want b with counts [2 0]", got.Rows[1])
	}
	if !got.Rows[2].IsOther || got.Rows[2].ClientIP != "" || got.Rows[2].Counts[0] != 1 || got.Rows[2].Counts[1] != 0 {
		t.Fatalf("Rows[2] = %+v, want Other with counts [1 0]", got.Rows[2])
	}
}

func TestLog_ClientActivity_NoOverflow_NoOtherRow(t *testing.T) {
	l := newTestLog(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)

	l.store.Record(Entry{Client: ClientInfo{IP: "a"}, Timestamp: from.Add(10 * time.Second)})
	waitForRowCount(t, l.store, 1)

	got, err := l.ClientActivity(context.Background(), from, to, time.Minute, 5)
	if err != nil {
		t.Fatalf("ClientActivity: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("len(Rows) = %d, want 1 (no Other row when nothing overflows topN), got %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0].IsOther {
		t.Fatalf("Rows[0] should not be IsOther: %+v", got.Rows[0])
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
