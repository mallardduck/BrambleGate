package mdnssd

import (
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestCache_Tick_NoActionBeforeFirstThreshold(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(70 * time.Second) // 70% of TTL, below the 80% threshold
	due, expired := c.Tick(clock.Now())

	if len(due) != 0 {
		t.Errorf("due = %v, want none before the 80%% threshold", due)
	}
	if len(expired) != 0 {
		t.Errorf("expired = %v, want none", expired)
	}
}

func TestCache_Tick_FiresRefreshAt80Percent(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(80 * time.Second)
	due, expired := c.Tick(clock.Now())

	if len(due) != 1 || due[0] != "_http._tcp.local." {
		t.Errorf("due = %v, want [\"_http._tcp.local.\"]", due)
	}
	if len(expired) != 0 {
		t.Errorf("expired = %v, want none — record is still within its TTL", expired)
	}
}

func TestCache_Tick_DoesNotRefireSameThresholdTwice(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(80 * time.Second)
	c.Tick(clock.Now()) // fires the 80% refresh

	clock.Advance(2 * time.Second) // still short of 85%
	due, _ := c.Tick(clock.Now())

	if len(due) != 0 {
		t.Errorf("due = %v, want none — already refreshed for this threshold", due)
	}
}

func TestCache_Tick_FiresEachThresholdOnce(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	var fired []int
	for _, pct := range []int{80, 85, 90, 95} {
		clock.Advance(time.Duration(pct)*time.Second - clock.Now().Sub(time.Unix(0, 0)))
		due, _ := c.Tick(clock.Now())
		if len(due) == 1 {
			fired = append(fired, pct)
		}
	}

	if len(fired) != 4 {
		t.Errorf("fired thresholds = %v, want all of [80 85 90 95]", fired)
	}
}

// This is the dnssd #63 regression, encoded directly: a record still due for
// refresh (i.e. within its TTL, even past the last refresh threshold) must
// NOT be evicted — only a record that gets no refresh answer by the time its
// full TTL elapses should be reported removed.
func TestCache_RecordSurvivesUntilTTLFullyElapsed(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(95 * time.Second) // past every refresh threshold, still < TTL
	_, expired := c.Tick(clock.Now())
	if len(expired) != 0 {
		t.Fatalf("expired = %v, want none — record is still within its TTL", expired)
	}

	clock.Advance(5 * time.Second) // now at 100% with no refresh answer received
	_, expired = c.Tick(clock.Now())
	if len(expired) != 1 || expired[0] != "foo._http._tcp.local." {
		t.Errorf("expired = %v, want the record evicted once its TTL fully elapses unanswered", expired)
	}
}

// A fresh Store() call (simulating a refresh answer, or any re-announcement)
// resets the refresh schedule and keeps the record alive well past what
// would have been its original expiry — this is the actual fix for dnssd
// #63's "live device silently evicted" behavior.
func TestCache_StoreResetsScheduleAndKeepsRecordAlive(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(80 * time.Second)
	c.Tick(clock.Now()) // refresh query goes out

	clock.Advance(5 * time.Second) // a response arrives at t=85s
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	clock.Advance(79 * time.Second) // t=164s: past the original t=100s expiry,
	due, expired := c.Tick(clock.Now())

	if len(expired) != 0 {
		t.Errorf("expired = %v, want none — the record was refreshed and should survive", expired)
	}
	if len(due) != 0 {
		t.Errorf("due = %v, want none — 79s since refresh is under 80%% of the new TTL", due)
	}
}

func TestCache_Remove_DropsRecordImmediately(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	c.Remove("foo._http._tcp.local.")

	due, expired := c.Tick(clock.Now())
	if len(due) != 0 || len(expired) != 0 {
		t.Errorf("due=%v expired=%v, want both empty — record was already removed", due, expired)
	}
}

func TestCache_Store_ReturnsWhetherNew(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)

	if isNew := c.Store("foo", "_http._tcp.local.", nil, 100*time.Second); !isNew {
		t.Error("first Store: isNew = false, want true")
	}
	if isNew := c.Store("foo", "_http._tcp.local.", nil, 100*time.Second); isNew {
		t.Error("second Store of same key: isNew = true, want false")
	}
}

func TestCache_KnownAnswers_ReturnsStoredRecordsForQuestion(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	rr := &dns.PTR{Hdr: rrHeader("_http._tcp.local.", dns.TypePTR, 100), Ptr: "Foo._http._tcp.local."}
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", rr, 100*time.Second)

	got := c.knownAnswersFor("_http._tcp.local.", clock.Now())

	if len(got) != 1 || got[0].RR != rr {
		t.Fatalf("KnownAnswers = %+v, want the stored record", got)
	}
}

func TestCache_KnownAnswers_OmitsRecordsWithNoRR(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", nil, 100*time.Second)

	got := c.knownAnswersFor("_http._tcp.local.", clock.Now())

	if len(got) != 0 {
		t.Errorf("KnownAnswers = %+v, want none — record was stored with a nil RR", got)
	}
}

func TestCache_KnownAnswers_FiltersByQuestion(t *testing.T) {
	clock := newFakeClock()
	c := NewCache(clock)
	c.Store("foo._http._tcp.local.", "_http._tcp.local.", &dns.PTR{Hdr: rrHeader("_http._tcp.local.", dns.TypePTR, 100)}, 100*time.Second)
	c.Store("bar._ssh._tcp.local.", "_ssh._tcp.local.", &dns.PTR{Hdr: rrHeader("_ssh._tcp.local.", dns.TypePTR, 100)}, 100*time.Second)

	got := c.knownAnswersFor("_http._tcp.local.", clock.Now())

	if len(got) != 1 || got[0].Question != "_http._tcp.local." {
		t.Fatalf("KnownAnswers = %+v, want only the _http._tcp.local. record", got)
	}
}
