package querylog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// globalStats is the single process-wide cheap-rollup accumulator (Phase
// 7c). Unlike Ring/Store it needs no settings-derived configuration, so
// there's no reload-driven lifecycle decision for it — one instance for
// the life of the process, fed directly by ServeDNS (handler.go) the same
// way Ring/Store are, and read through Log below.
var globalStats = newStatsRollup(statsBucketWidth, statsBucketCount)

// Log is the read-side facade over Ring (hot, live), Store (durable,
// historical), and the cheap in-memory rollups — the single thing GUI code
// should query against, per dev-docs/query-log.md's Phase 7c design
// discussion: callers ask a question, Log decides whether it's a live/ring
// read, an in-memory rollup, or a Store SQL query, without the caller
// needing to know there are two storage tiers.
//
// CurrentLog always composes the same process-wide singletons
// (Current/CurrentStore/globalStats); the fields are unexported so
// production code goes through CurrentLog rather than assembling its own
// Log by hand — tests within this package construct one directly around a
// scratch Ring/Store/statsRollup instead.
type Log struct {
	ring  *Ring
	store *Store
	stats *statsRollup
}

// CurrentLog returns a Log composing the process-wide Ring, Store (nil if
// persistence isn't configured), and the shared stats rollup.
func CurrentLog() *Log {
	return &Log{ring: Current(), store: CurrentStore(), stats: globalStats}
}

// LiveSnapshot reads only Ring — never Store — so the live Query Log page
// stays fast regardless of history size (dev-docs/query-log.md's Phase 7a
// guarantee, preserved unchanged by this facade). A nil Ring (querylog
// never loaded) reports no entries, not an error.
func (l *Log) LiveSnapshot(f Filter) []Entry {
	if l == nil || l.ring == nil {
		return nil
	}
	return l.ring.Snapshot(f)
}

// Totals returns the cheap "since process boot" rollups.
func (l *Log) Totals() Totals {
	if l == nil || l.stats == nil {
		return Totals{}
	}
	return l.stats.totals()
}

// RecentSeries returns a dense, fixed-length query-volume time series
// covering the trailing statsBucketCount*statsBucketWidth window (24h at
// the current defaults) — no query, backed entirely by the in-memory
// bucket ring.
func (l *Log) RecentSeries() []Bucket {
	if l == nil || l.stats == nil {
		return nil
	}
	return l.stats.recentSeries(time.Now())
}

// ErrStoreNotConfigured is returned by Log's Store-backed queries
// (TopDomains, TopClients, Series) when Query Log's durable persistence
// isn't on.
var ErrStoreNotConfigured = errors.New("querylog: persistence is not configured (Query Log's durable store is off)")

// DomainCount is one row of a TopDomains result.
type DomainCount struct {
	QName string
	Count int64
}

// ClientCount is one row of a TopClients result — grouped by (client IP,
// VLAN) rather than IP alone: the same IP reused across VLANs (e.g. after
// a reconfigured client moves) is tracked separately, consistent with
// dev-docs/query-log.md's VLAN-awareness differentiator. VLAN here is
// whatever string was resolved at query time — a later VLAN rename/removal
// in settings does not retroactively relabel or invalidate historical
// rows (dev-docs/query-log.md's Phase 7c design notes).
type ClientCount struct {
	ClientIP string
	VLAN     string
	Count    int64
}

// TopDomains returns the n most-queried domains in the trailing window,
// read from Store. Unlike Totals/RecentSeries, this is deliberately never
// an in-memory rollup: qname cardinality is unbounded over a long uptime,
// unlike the small fixed key spaces Totals tracks, so an in-memory map
// here would be an unbounded-memory-growth risk.
func (l *Log) TopDomains(ctx context.Context, window time.Duration, n int) ([]DomainCount, error) {
	if l == nil || l.store == nil {
		return nil, ErrStoreNotConfigured
	}
	cutoff := time.Now().Add(-window).UnixMilli()
	rows, err := l.store.db.QueryContext(ctx,
		`SELECT qname, COUNT(*) c FROM queries WHERE ts_unix_ms >= ? GROUP BY qname ORDER BY c DESC LIMIT ?`,
		cutoff, n,
	)
	if err != nil {
		return nil, fmt.Errorf("querylog: top domains: %w", err)
	}
	defer rows.Close()

	var out []DomainCount
	for rows.Next() {
		var d DomainCount
		if err := rows.Scan(&d.QName, &d.Count); err != nil {
			return nil, fmt.Errorf("querylog: top domains: scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TopClients returns the n most active (client IP, VLAN) pairs in the
// trailing window, read from Store.
func (l *Log) TopClients(ctx context.Context, window time.Duration, n int) ([]ClientCount, error) {
	if l == nil || l.store == nil {
		return nil, ErrStoreNotConfigured
	}
	cutoff := time.Now().Add(-window).UnixMilli()
	rows, err := l.store.db.QueryContext(ctx,
		`SELECT client_ip, vlan, COUNT(*) c FROM queries WHERE ts_unix_ms >= ? GROUP BY client_ip, vlan ORDER BY c DESC LIMIT ?`,
		cutoff, n,
	)
	if err != nil {
		return nil, fmt.Errorf("querylog: top clients: %w", err)
	}
	defer rows.Close()

	var out []ClientCount
	for rows.Next() {
		var c ClientCount
		if err := rows.Scan(&c.ClientIP, &c.VLAN, &c.Count); err != nil {
			return nil, fmt.Errorf("querylog: top clients: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientActivityRow is one series in a ClientActivity result: a client's
// (or the folded "Other" bucket's) query volume across the same buckets as
// ClientActivitySeries.Buckets, dense/zero-filled like Series. IsOther is
// true for the single trailing row folding every client outside the
// top-N — ClientIP/VLAN are empty on that row, same convention as
// vlanBarLabel's "(none)" handling for an unmatched VLAN, but left to the
// caller to label since querylog stays display-agnostic.
type ClientActivityRow struct {
	ClientIP string
	VLAN     string
	IsOther  bool
	Counts   []int64
}

// ClientActivitySeries is ClientActivity's result: a dense set of bucket
// starts shared by every row, plus one row per top-N client and (if any
// queries fell outside the top-N) a final IsOther row.
type ClientActivitySeries struct {
	Buckets []time.Time
	Rows    []ClientActivityRow
}

// ClientActivity returns a stacked time series of query volume for the
// topN most active (client IP, VLAN) pairs in [from,to), bucketed at the
// given width, with every other client folded into a trailing "Other" row —
// the per-client breakdown pihole's dashboard calls "Client Activity."
// Store-backed only, like TopDomains/TopClients: client-level granularity
// isn't in the cheap in-memory rollup (stats.go's doc comment on why).
func (l *Log) ClientActivity(ctx context.Context, from, to time.Time, bucket time.Duration, topN int) (ClientActivitySeries, error) {
	if l == nil || l.store == nil {
		return ClientActivitySeries{}, ErrStoreNotConfigured
	}
	if !to.After(from) {
		return ClientActivitySeries{}, errors.New("querylog: client activity: to must be after from")
	}
	bucketMs := bucket.Milliseconds()
	if bucketMs <= 0 {
		return ClientActivitySeries{}, errors.New("querylog: client activity: bucket must be positive")
	}

	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	rows, err := l.store.db.QueryContext(ctx,
		`SELECT (ts_unix_ms / ?) AS b, client_ip, vlan, COUNT(*) c FROM queries
		 WHERE ts_unix_ms >= ? AND ts_unix_ms < ? GROUP BY b, client_ip, vlan`,
		bucketMs, fromMs, toMs,
	)
	if err != nil {
		return ClientActivitySeries{}, fmt.Errorf("querylog: client activity: %w", err)
	}
	defer rows.Close()

	type clientKey struct{ ip, vlan string }
	perBucket := make(map[clientKey]map[int64]int64)
	totals := make(map[clientKey]int64)
	for rows.Next() {
		var b int64
		var k clientKey
		var c int64
		if err := rows.Scan(&b, &k.ip, &k.vlan, &c); err != nil {
			return ClientActivitySeries{}, fmt.Errorf("querylog: client activity: scan: %w", err)
		}
		if perBucket[k] == nil {
			perBucket[k] = make(map[int64]int64)
		}
		perBucket[k][b] = c
		totals[k] += c
	}
	if err := rows.Err(); err != nil {
		return ClientActivitySeries{}, err
	}

	top := make([]clientKey, 0, len(totals))
	for k := range totals {
		top = append(top, k)
	}
	sort.Slice(top, func(i, j int) bool {
		if totals[top[i]] != totals[top[j]] {
			return totals[top[i]] > totals[top[j]]
		}
		if top[i].ip != top[j].ip {
			return top[i].ip < top[j].ip
		}
		return top[i].vlan < top[j].vlan
	})
	var other []clientKey
	if len(top) > topN {
		top, other = top[:topN], top[topN:]
	}

	fromBucket := fromMs / bucketMs
	lastBucket := (toMs - 1) / bucketMs
	n := int(lastBucket - fromBucket + 1)
	buckets := make([]time.Time, n)
	for i := range n {
		buckets[i] = time.UnixMilli((fromBucket + int64(i)) * bucketMs).UTC()
	}

	out := ClientActivitySeries{Buckets: buckets, Rows: make([]ClientActivityRow, 0, len(top)+1)}
	for _, k := range top {
		row := ClientActivityRow{ClientIP: k.ip, VLAN: k.vlan, Counts: make([]int64, n)}
		for i := range n {
			row.Counts[i] = perBucket[k][fromBucket+int64(i)]
		}
		out.Rows = append(out.Rows, row)
	}
	if len(other) > 0 {
		row := ClientActivityRow{IsOther: true, Counts: make([]int64, n)}
		for _, k := range other {
			for i := range n {
				row.Counts[i] += perBucket[k][fromBucket+int64(i)]
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// Series returns a dense, zero-filled query-volume time series over
// [from, to) bucketed at the given width, read from Store — for windows
// longer than RecentSeries covers, or spanning a restart.
func (l *Log) Series(ctx context.Context, from, to time.Time, bucket time.Duration) ([]Bucket, error) {
	if l == nil || l.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !to.After(from) {
		return nil, errors.New("querylog: series: to must be after from")
	}
	bucketMs := bucket.Milliseconds()
	if bucketMs <= 0 {
		return nil, errors.New("querylog: series: bucket must be positive")
	}

	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	rows, err := l.store.db.QueryContext(ctx,
		`SELECT (ts_unix_ms / ?) AS b, COUNT(*) c FROM queries WHERE ts_unix_ms >= ? AND ts_unix_ms < ? GROUP BY b ORDER BY b`,
		bucketMs, fromMs, toMs,
	)
	if err != nil {
		return nil, fmt.Errorf("querylog: series: %w", err)
	}
	defer rows.Close()

	counts := make(map[int64]int64)
	for rows.Next() {
		var b, c int64
		if err := rows.Scan(&b, &c); err != nil {
			return nil, fmt.Errorf("querylog: series: scan: %w", err)
		}
		counts[b] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fromBucket := fromMs / bucketMs
	lastBucket := (toMs - 1) / bucketMs
	n := int(lastBucket - fromBucket + 1)
	out := make([]Bucket, n)
	for i := range n {
		b := fromBucket + int64(i)
		out[i] = Bucket{
			Start: time.UnixMilli(b * bucketMs).UTC(),
			Count: counts[b],
		}
	}
	return out, nil
}
