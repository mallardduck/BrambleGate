package mdnssd

import "time"

// Entry is a discovered mDNS-SD service instance.
type Entry struct {
	Host      string // hostname, e.g. "foo.local" (no trailing dot)
	Instance  string // instance label, e.g. "Foo"
	Type      string // e.g. "_http._tcp"
	Domain    string // e.g. "local"
	TXT       map[string]string
	IPv4      []string
	IPv6      []string
	IfaceName string
	// TTL is the PTR record's own announced TTL, as last (re)stored by
	// Cache — the real, authoritative liveness window this device
	// advertised, not a value this package invents. Callers that track
	// their own liveness/expiry downstream should use this as the source
	// of truth rather than a separately-chosen constant, to avoid a second,
	// potentially-shorter-than-reality TTL silently expiring an entry
	// mdnssd is still correctly refreshing.
	TTL time.Duration
}

// AddFunc is called when a service instance is newly discovered, or when
// previously-missing data about it (address, TXT) arrives.
type AddFunc func(Entry)

// RmvFunc is called when a service instance is no longer present: a
// goodbye packet (TTL=0), or its TTL fully elapsing with no refresh answer.
type RmvFunc func(Entry)

// TypeFunc is called once per distinct service type discovered by
// BrowseTypes, e.g. "_http._tcp".
type TypeFunc func(serviceType string)
