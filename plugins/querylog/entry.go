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
	Verdict   string // "local", "cached", "coalesced", "forwarded", "nxdomain", ...
	Source    string // "localrecords", "mdnsbridge", "hosts", "cache", "vlancache", "forward", ...
	Rcode     int
	Latency   time.Duration

	// CacheOutcome is a cache-subsystem-specific dimension, independent of
	// Verdict/Source: plugins/vlancache sets it ("hit", "coalesced", "miss")
	// on every query it handles, even ones it must NOT claim as Source
	// "vlancache" (a genuine miss that falls through to a real forward stays
	// unattributed in Source/Verdict — see plugins/vlancache/handler.go —
	// but is still a "miss" here). Lets the dashboard show a full breakdown
	// of vlancache's own activity without polluting the general Source
	// breakdown with misses that are really forwards.
	//
	// QueryLog.ServeDNS defaults this to "none" for any query that never
	// reached the cache layer at all (e.g. localrecords/mdnsbridge answered
	// directly and never called Next) — a real, explicit enum value rather
	// than leaving the Go zero value "" to stand in for it, since a raw ""
	// key in the rollup/JSON output (querylog.Totals.ByCacheOutcome) reads
	// as "unset/broken" rather than "deliberately not applicable."
	CacheOutcome string

	// Listener is the local address the query arrived on (w.LocalAddr()),
	// e.g. "0.0.0.0:53" — answers "what port did this come through" without
	// querylog needing model's listener/transport vocabulary (it deliberately
	// stays independent of model, same as localrecords/mdnsbridge —
	// dev-docs/repo-layout.md). The GUI, which already has
	// Settings.Listeners, maps the port back to a friendly transport label
	// (DoT/DoH/...) purely for display.
	Listener string
	// Proto is "udp" or "tcp" (request.Request.Proto()).
	Proto string
	// AuthenticatedData mirrors the response's AD flag: whether the upstream
	// claims the answer validated. Not independent DNSSEC validation by
	// BrambleGate itself — the dnssec plugin isn't wired into the chain
	// (internal/engine/directives.go's reserved-but-unused slot).
	AuthenticatedData bool
	// AnswerType is the RR type name of the first answer record (e.g. "A",
	// "CNAME") — empty when there's no answer (NXDOMAIN/NODATA).
	AnswerType string
}
