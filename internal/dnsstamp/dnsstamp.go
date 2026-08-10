// Package dnsstamp generates DNS Stamps (sdns://…) for BrambleGate's own
// DoH/DoT listeners — a portable, copy-pasteable string some clients (e.g.
// dnscrypt-proxy) accept instead of separate hostname/port/path fields.
//
// Uses github.com/jedisct1/go-dnsstamps — the reference implementation from
// the DNS Stamps spec's own author — rather than hand-rolling the binary
// encoding ourselves; this is a byte-level wire format real external client
// software parses, so correctness matters more than avoiding a small
// dependency (see dev-docs/certificates.md).
package dnsstamp

import (
	"strconv"

	"github.com/jedisct1/go-dnsstamps"
)

// DoH returns an sdns:// stamp for a DoH listener at domain:port ("" if
// domain is empty). ServerAddrStr is deliberately left empty — the stamp
// doesn't pin an IP, so the client resolves domain itself, which (per
// dev-docs/certificates.md's ACME self-record) resolves to whichever LAN IP
// is correct for the client's own network. Pinning one IP here would be wrong
// for every network except whichever got baked in, same reasoning as DDR's
// deliberately-omitted glue records.
func DoH(domain string, port int) string {
	if domain == "" {
		return ""
	}
	stamp := dnsstamps.ServerStamp{
		Proto:        dnsstamps.StampProtoTypeDoH,
		ProviderName: hostWithPort(domain, port, 443),
		Path:         "/dns-query",
	}
	return stamp.String()
}

// DoH3 returns an sdns:// stamp for a DoH3 (HTTP/3) listener at domain:port
// ("" if domain is empty). The DNS Stamps spec has no separate proto type for
// HTTP/3 — a DoH stamp only names the query endpoint, and h2 vs. h3 is
// negotiated at the TLS/ALPN layer the same way any HTTPS client picks a
// transport, so this reuses StampProtoTypeDoH. Same no-pinned-address
// reasoning as DoH.
func DoH3(domain string, port int) string {
	if domain == "" {
		return ""
	}
	stamp := dnsstamps.ServerStamp{
		Proto:        dnsstamps.StampProtoTypeDoH,
		ProviderName: hostWithPort(domain, port, 443),
		Path:         "/dns-query",
	}
	return stamp.String()
}

// DoT returns an sdns:// stamp for a DoT listener at domain:port ("" if
// domain is empty). Same no-pinned-address reasoning as DoH.
func DoT(domain string, port int) string {
	if domain == "" {
		return ""
	}
	stamp := dnsstamps.ServerStamp{
		Proto:        dnsstamps.StampProtoTypeTLS,
		ProviderName: hostWithPort(domain, port, 853),
	}
	return stamp.String()
}

// DoHURL returns the plain https:// query URL for a DoH listener at
// domain:port ("" if domain is empty) — for clients/configs that want a
// human-readable URL instead of an sdns:// stamp.
func DoHURL(domain string, port int) string {
	if domain == "" {
		return ""
	}
	return "https://" + hostWithPort(domain, port, 443) + "/dns-query"
}

// DoTURL returns the plain tls:// query URL for a DoT listener at
// domain:port ("" if domain is empty).
func DoTURL(domain string, port int) string {
	if domain == "" {
		return ""
	}
	return "tls://" + hostWithPort(domain, port, 853)
}

// DoH3URL returns the plain https:// query URL for a DoH3 listener at
// domain:port ("" if domain is empty). Identical in form to DoHURL — the URL
// itself doesn't encode HTTP/2 vs. HTTP/3, only the port (which usually
// differs from DoH's, per docs/deploying-docker.md's port-sharing note).
func DoH3URL(domain string, port int) string {
	if domain == "" {
		return ""
	}
	return "https://" + hostWithPort(domain, port, 443) + "/dns-query"
}

// DoQURL returns the plain quic:// query URL for a DoQ listener at
// domain:port ("" if domain is empty).
func DoQURL(domain string, port int) string {
	if domain == "" {
		return ""
	}
	return "quic://" + hostWithPort(domain, port, 853)
}

// hostWithPort appends :port only when it differs from the protocol default
// — go-dnsstamps strips a redundant default-port suffix itself, but writing
// it unconditionally would make every stamp needlessly longer.
func hostWithPort(domain string, port, defaultPort int) string {
	if port == 0 || port == defaultPort {
		return domain
	}
	return domain + ":" + strconv.Itoa(port)
}
