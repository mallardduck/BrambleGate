package querylog

import "time"

// ClientInfo identifies the querying client.
type ClientInfo struct {
	IP   string
	VLAN string // "" when the client's address matched no configured VLAN
}

// Entry is one observed DNS query, from the initial request through to the
// answer written back to the client. Verdict/Source are open string enums
// (not fixed booleans) so a future feature (e.g. a blocklist plugin) can
// contribute new members without a schema migration — see dev-docs/query-log.md.
type Entry struct {
	Timestamp time.Time
	Client    ClientInfo
	QName     string
	QType     uint16
	Verdict   string // "local", "cached", "forwarded", "nxdomain", ...
	Source    string // "localrecords", "mdnsbridge", "cache", "forward", ...
	Rcode     int
	Latency   time.Duration
}
