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

// hostWithPort appends :port only when it differs from the protocol default
// — go-dnsstamps strips a redundant default-port suffix itself, but writing
// it unconditionally would make every stamp needlessly longer.
func hostWithPort(domain string, port, defaultPort int) string {
	if port == 0 || port == defaultPort {
		return domain
	}
	return domain + ":" + strconv.Itoa(port)
}
