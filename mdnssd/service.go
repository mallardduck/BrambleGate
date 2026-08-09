package mdnssd

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
}

// AddFunc is called when a service instance is newly discovered, or when
// previously-missing data about it (address, TXT) arrives.
type AddFunc func(Entry)

// RmvFunc is called when a service instance is no longer present: a
// goodbye packet (TTL=0), or its TTL fully elapsing with no refresh answer.
type RmvFunc func(Entry)
