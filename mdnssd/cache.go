package mdnssd

import (
	"time"

	"github.com/miekg/dns"
)

// refreshThresholds are the fractions of a record's TTL at which RFC 6762
// §5.2 says a querier should proactively re-query to keep a live record from
// expiring — 80%, 85%, 90%, and 95%. dnssd (github.com/brutella/dnssd,
// issue #63) never does this: it queries once at startup and otherwise only
// listens, so live records are silently evicted the moment their TTL runs
// out. Cache exists to fix that.
var refreshThresholds = [4]float64{0.80, 0.85, 0.90, 0.95}

type cacheRecord struct {
	rr            dns.RR // the record as last (re)stored; nil if the caller didn't supply one
	question      string // query name to reissue when refreshing this record
	ttl           time.Duration
	storedAt      time.Time
	refreshesSent int // how many of refreshThresholds have already fired
}

func (r *cacheRecord) elapsed(now time.Time) time.Duration { return now.Sub(r.storedAt) }

func (r *cacheRecord) expired(now time.Time) bool { return r.elapsed(now) >= r.ttl }

// dueThreshold reports whether a new refresh threshold has been crossed as
// of now, advancing refreshesSent past every threshold now covered so it
// isn't reported again. It does not check for full expiry — call expired
// separately.
func (r *cacheRecord) dueThreshold(now time.Time) bool {
	fired := false
	elapsed := r.elapsed(now)
	for r.refreshesSent < len(refreshThresholds) {
		thresholdAt := time.Duration(float64(r.ttl) * refreshThresholds[r.refreshesSent])
		if elapsed < thresholdAt {
			break
		}
		r.refreshesSent++
		fired = true
	}
	return fired
}

// Cache tracks TTL'd records by an opaque key and drives RFC 6762 §5.2
// active refresh (see refreshThresholds): callers should re-query due
// records rather than waiting for them to silently expire.
type Cache struct {
	clock   Clock
	records map[string]*cacheRecord
}

// NewCache returns an empty Cache driven by clock (use a real Clock in
// production, a fake one in tests).
func NewCache(clock Clock) *Cache {
	return &Cache{clock: clock, records: make(map[string]*cacheRecord)}
}

// Store (re)records key as freshly seen with the given RR/TTL/query name,
// resetting its refresh schedule. rr may be nil if the caller has no record
// to offer as a future known answer (see KnownAnswers) — the TTL/refresh
// bookkeeping doesn't need it. Returns true if key is new, false if it
// refreshes an existing record.
func (c *Cache) Store(key, question string, rr dns.RR, ttl time.Duration) bool {
	_, existed := c.records[key]
	c.records[key] = &cacheRecord{rr: rr, question: question, ttl: ttl, storedAt: c.clock.Now()}
	return !existed
}

// knownAnswersFor returns knownRecord values (see knownanswer.go) for every
// cached record answering question that was stored with a non-nil rr, as of
// now. Callers pass this to the package-level knownAnswers() to build a
// re-query's RFC 6762 §7.1 known-answer list. Unexported: internal only,
// nothing outside this package needs it.
func (c *Cache) knownAnswersFor(question string, now time.Time) []knownRecord {
	var out []knownRecord
	for _, rec := range c.records {
		if rec.question != question || rec.rr == nil {
			continue
		}
		out = append(out, knownRecord{RR: rec.rr, Question: rec.question, TTL: rec.ttl, Elapsed: rec.elapsed(now)})
	}
	return out
}

// Remove deletes a record immediately, e.g. on a goodbye packet (TTL=0).
func (c *Cache) Remove(key string) {
	delete(c.records, key)
}

// Tick advances the cache to now and reports:
//   - due: query names for records that just crossed a refresh threshold
//     and should be re-queried to confirm they're still alive.
//   - expired: keys whose TTL fully elapsed with no refresh answer — these
//     are removed from the cache and should be reported as gone.
func (c *Cache) Tick(now time.Time) (due []string, expired []string) {
	for key, rec := range c.records {
		if rec.expired(now) {
			expired = append(expired, key)
			delete(c.records, key)
			continue
		}
		if rec.dueThreshold(now) {
			due = append(due, rec.question)
		}
	}
	return due, expired
}
