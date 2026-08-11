package querylog

import (
	"testing"
	"time"
)

func TestStatsRollup_Totals_Empty(t *testing.T) {
	s := newStatsRollup(time.Minute, 10)
	got := s.totals()
	if got.Queries != 0 || len(got.ByVerdict) != 0 || len(got.BySource) != 0 || len(got.ByVLAN) != 0 || len(got.ByQType) != 0 {
		t.Fatalf("expected an empty Totals, got %+v", got)
	}
}

func TestStatsRollup_Observe_AccumulatesCounts(t *testing.T) {
	s := newStatsRollup(time.Minute, 10)
	now := time.Now()

	s.observe(Entry{Verdict: "local", Source: "localrecords", Client: ClientInfo{VLAN: "trusted"}, QType: 1, Timestamp: now})
	s.observe(Entry{Verdict: "forwarded", Source: "forward", Client: ClientInfo{VLAN: "iot"}, QType: 28, Timestamp: now})
	s.observe(Entry{Verdict: "forwarded", Source: "forward", Client: ClientInfo{VLAN: "iot"}, QType: 28, Timestamp: now})

	got := s.totals()
	if got.Queries != 3 {
		t.Fatalf("Queries = %d, want 3", got.Queries)
	}
	if got.ByVerdict["local"] != 1 || got.ByVerdict["forwarded"] != 2 {
		t.Fatalf("ByVerdict = %+v, want local:1 forwarded:2", got.ByVerdict)
	}
	if got.BySource["localrecords"] != 1 || got.BySource["forward"] != 2 {
		t.Fatalf("BySource = %+v, want localrecords:1 forward:2", got.BySource)
	}
	if got.ByVLAN["trusted"] != 1 || got.ByVLAN["iot"] != 2 {
		t.Fatalf("ByVLAN = %+v, want trusted:1 iot:2", got.ByVLAN)
	}
	if got.ByQType[1] != 1 || got.ByQType[28] != 2 {
		t.Fatalf("ByQType = %+v, want 1:1 28:2", got.ByQType)
	}
}

func TestStatsRollup_Totals_ReturnsIndependentCopies(t *testing.T) {
	s := newStatsRollup(time.Minute, 10)
	s.observe(Entry{Verdict: "local"})

	got := s.totals()
	got.ByVerdict["local"] = 999 // mutate the caller's copy

	again := s.totals()
	if again.ByVerdict["local"] != 1 {
		t.Fatalf("mutating a returned Totals leaked into the rollup: ByVerdict[local] = %d, want 1", again.ByVerdict["local"])
	}
}

func TestStatsRollup_RecentSeries_DenseAndOrdered(t *testing.T) {
	s := newStatsRollup(time.Minute, 5)
	now := time.Now()

	got := s.recentSeries(now)
	if len(got) != 5 {
		t.Fatalf("len(RecentSeries) = %d, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Start.After(got[i-1].Start) {
			t.Fatalf("buckets not strictly increasing at index %d: %v then %v", i, got[i-1].Start, got[i].Start)
		}
	}
	for _, b := range got {
		if b.Count != 0 {
			t.Fatalf("expected an all-zero series with nothing observed, got %+v", b)
		}
	}
}

func TestStatsRollup_RecentSeries_CountsLandInCorrectBucket(t *testing.T) {
	s := newStatsRollup(time.Minute, 5)
	now := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC) // mid-minute, so truncation is exercised

	s.observe(Entry{Timestamp: now})
	s.observe(Entry{Timestamp: now})
	s.observe(Entry{Timestamp: now.Add(-2 * time.Minute)})

	got := s.recentSeries(now)
	total := int64(0)
	for _, b := range got {
		total += b.Count
	}
	if total != 3 {
		t.Fatalf("total across buckets = %d, want 3", total)
	}
	if got[len(got)-1].Count != 2 {
		t.Fatalf("last (current) bucket count = %d, want 2", got[len(got)-1].Count)
	}
}

func TestStatsRollup_RecentSeries_WraparoundDoesNotLeakStaleCounts(t *testing.T) {
	// A tiny ring (2 buckets) makes wraparound trivial to trigger: observing
	// at t0 and then again 2*bucketWidth later must land in the same slot
	// index but must NOT accumulate onto the stale t0 count, since that
	// slot's data is now outside the retained window.
	s := newStatsRollup(time.Minute, 2)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	s.observe(Entry{Timestamp: t0})
	later := t0.Add(4 * time.Minute) // several laps around a 2-bucket ring
	s.observe(Entry{Timestamp: later})

	got := s.recentSeries(later)
	if got[len(got)-1].Count != 1 {
		t.Fatalf("current bucket count = %d, want 1 (stale t0 count must not leak in)", got[len(got)-1].Count)
	}
}

func TestStatsRollup_ConcurrentObserve(t *testing.T) {
	s := newStatsRollup(time.Minute, 10)
	done := make(chan struct{})
	const n = 100
	for i := 0; i < n; i++ {
		go func() {
			s.observe(Entry{Verdict: "local", Timestamp: time.Now()})
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	if got := s.totals().Queries; got != n {
		t.Fatalf("Queries = %d, want %d", got, n)
	}
}
