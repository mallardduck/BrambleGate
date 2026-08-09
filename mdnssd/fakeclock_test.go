package mdnssd

import "time"

// fakeClock is a Clock test double: time only moves when Advance is called,
// so refresh-scheduling tests are deterministic instead of racing real time.
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }
