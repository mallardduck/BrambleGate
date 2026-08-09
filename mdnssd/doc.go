// Package mdnssd is a from-scratch mDNS/DNS-SD browsing library (RFC 6762,
// RFC 6763). It covers browsing only — discovering service instances and
// service types on the local network — not advertising/responding.
//
// It exists because github.com/brutella/dnssd, which this project used
// previously, has two gaps that matter for a generic mDNS browser:
//
//   - No way to browse the DNS-SD meta-query (_services._dns-sd._udp), so
//     unlisted service types can never be discovered (dnssd issue #20).
//   - No active cache refresh per RFC 6762 §5.2: it queries once at startup
//     and then only listens passively, so live entries silently expire and
//     fire spurious removals (dnssd issue #63).
//
// mdnssd fixes both by design: BrowseTypes treats the meta-query as a first
// -class case (mirroring how Rust's mdns-sd crate behaves), and the cache
// proactively re-queries at 80/85/90/95% of each record's TTL before
// evicting it.
package mdnssd
