package querylog

import (
	"sync"
	"time"
)

// statsBucketWidth/statsBucketCount size the cheap in-memory "recent
// series" ring: 10-minute buckets, 144 of them, covering a trailing 24h —
// dev-docs/query-log.md's Phase 7c defaults.
const (
	statsBucketWidth = 10 * time.Minute
	statsBucketCount = 144
)

// Totals is a snapshot of statsRollup's cheap, in-memory-only counters —
// "since this process booted," same convention as Ring, never reset by a
// reload/settings change. Every map here has a small, bounded key space (a
// handful of Verdict/Source values, VLANs bounded by settings, QType
// capped at 65536) — deliberately unlike qname/client-IP, which are NOT
// tracked here; see Log.TopDomains/TopClients for why those are SQL-only.
type Totals struct {
	Queries   int64            `json:"queries"`
	ByVerdict map[string]int64 `json:"by_verdict"`
	BySource  map[string]int64 `json:"by_source"`
	ByVLAN    map[string]int64 `json:"by_vlan"`
	ByQType   map[uint16]int64 `json:"by_qtype"`
	// ByCacheOutcome mirrors Entry.CacheOutcome — see its doc comment.
	// "none" entries (queries the cache layer never saw) are counted like
	// every other rollup here; callers rendering this as a chart should
	// skip the "none" key the same way they'd skip any other zero-signal
	// bucket (dashboard_stats.templ's nonEmptyCounts).
	ByCacheOutcome map[string]int64 `json:"by_cache_outcome"`
}

// Bucket is one point in a query-volume time series (Log.RecentSeries/Series).
type Bucket struct {
	Start time.Time `json:"start"`
	Count int64     `json:"count"`
}

// statsRollup accumulates the cheap rollups every Entry feeds, alongside
// Ring/Store (dev-docs/query-log.md's Phase 7c). Unlike Ring/Store, it
// takes no settings-derived configuration, so there's no Corefile-parse
// lifecycle decision to make for it — one instance lives for the whole
// process (see the package-level globalStats var in log.go) and is never
// recreated on reload.
type statsRollup struct {
	mu sync.Mutex

	queries        int64
	byVerdict      map[string]int64
	bySource       map[string]int64
	byVLAN         map[string]int64
	byQType        map[uint16]int64
	byCacheOutcome map[string]int64

	bucketWidth time.Duration
	buckets     []statsBucket
}

// statsBucket is one slot of the fixed-size recent-series ring. epoch (the
// slot's own bucket-start time, truncated to bucketWidth) is stored
// alongside count so a write can tell "this slot's data is from the
// current window" apart from "this slot was last touched over
// len(buckets)*bucketWidth ago and is now stale" — without it, wraparound
// after 24h of uptime would silently accumulate onto leftover data from a
// much earlier lap around the ring.
type statsBucket struct {
	epoch int64 // unix seconds, truncated to bucketWidth
	count int64
}

func newStatsRollup(bucketWidth time.Duration, bucketCount int) *statsRollup {
	return &statsRollup{
		byVerdict:      make(map[string]int64),
		bySource:       make(map[string]int64),
		byVLAN:         make(map[string]int64),
		byQType:        make(map[uint16]int64),
		byCacheOutcome: make(map[string]int64),
		bucketWidth:    bucketWidth,
		buckets:        make([]statsBucket, bucketCount),
	}
}

// observe feeds e into every cheap rollup. Called once per completed
// query, alongside Ring.Push/Store.Record (handler.go) — same Entry
// stream, no second attribution path.
func (s *statsRollup) observe(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.queries++
	s.byVerdict[e.Verdict]++
	s.bySource[e.Source]++
	s.byVLAN[e.Client.VLAN]++
	s.byQType[e.QType]++
	s.byCacheOutcome[e.CacheOutcome]++

	epoch := bucketEpoch(e.Timestamp, s.bucketWidth)
	idx := bucketIndex(epoch, s.bucketWidth, len(s.buckets))
	b := &s.buckets[idx]
	if b.epoch != epoch {
		b.epoch = epoch
		b.count = 0
	}
	b.count++
}

// totals returns a snapshot safe to hand to a caller — every map is a
// copy, so the caller can't observe (or corrupt) concurrent updates.
func (s *statsRollup) totals() Totals {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Totals{
		Queries:        s.queries,
		ByVerdict:      copyStringCounts(s.byVerdict),
		BySource:       copyStringCounts(s.bySource),
		ByVLAN:         copyStringCounts(s.byVLAN),
		ByQType:        copyQTypeCounts(s.byQType),
		ByCacheOutcome: copyStringCounts(s.byCacheOutcome),
	}
}

// recentSeries returns exactly len(buckets) points, oldest first, ending
// at the bucket containing now. A slot whose stored epoch doesn't match
// the bucket it's being read as (never written, or stale from a previous
// lap) reports a zero count rather than being omitted — callers get a
// dense series safe to chart directly.
func (s *statsRollup) recentSeries(now time.Time) []Bucket {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.buckets)
	widthSec := int64(s.bucketWidth / time.Second)
	nowEpoch := bucketEpoch(now, s.bucketWidth)

	out := make([]Bucket, n)
	for i := range n {
		epoch := nowEpoch - int64(n-1-i)*widthSec
		idx := bucketIndex(epoch, s.bucketWidth, n)
		b := s.buckets[idx]
		count := int64(0)
		if b.epoch == epoch {
			count = b.count
		}
		out[i] = Bucket{Start: time.Unix(epoch, 0).UTC(), Count: count}
	}
	return out
}

func bucketEpoch(t time.Time, width time.Duration) int64 {
	sec := t.Unix()
	w := int64(width / time.Second)
	return sec - (sec % w)
}

func bucketIndex(epoch int64, width time.Duration, n int) int {
	w := int64(width / time.Second)
	slot := (epoch / w) % int64(n)
	if slot < 0 {
		slot += int64(n)
	}
	return int(slot)
}

func copyStringCounts(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyQTypeCounts(m map[uint16]int64) map[uint16]int64 {
	out := make(map[uint16]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
