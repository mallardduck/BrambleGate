package mdnssd

import "time"

// Clock abstracts time so refresh-scheduling logic (cache.go) is testable
// without real wall-clock waits.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
