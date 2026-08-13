package clientnames

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// Resolver looks up a reverse-DNS (PTR) name for ip against a specific
// upstream. This is the tier-2 lookup (dev-docs/client-names.md) — kept as
// an interface so Table can be unit tested without a real network round trip.
type Resolver interface {
	Lookup(ctx context.Context, ip string) (string, error)
}

// dnsResolver is the real Resolver, querying a fixed upstream
// (client_names.ptr_upstream) directly — deliberately never the general
// upstream_dns, which usually has no idea what's on the LAN and would just
// NXDOMAIN a reverse query (dev-docs/client-names.md's "PTR upstream"
// section). This is the one narrow, deliberately-scoped conditional-forward
// this project needs.
type dnsResolver struct {
	addr   string
	client *dns.Client
}

// NewPTRResolver returns a Resolver that queries addr (host:port) for PTR
// records.
func NewPTRResolver(addr string) Resolver {
	return &dnsResolver{addr: addr, client: &dns.Client{Timeout: 2 * time.Second}}
}

func (r *dnsResolver) Lookup(ctx context.Context, ip string) (string, error) {
	name, err := dns.ReverseAddr(ip)
	if err != nil {
		return "", fmt.Errorf("reverse address for %q: %w", ip, err)
	}
	m := new(dns.Msg)
	m.SetQuestion(name, dns.TypePTR)
	in, _, err := r.client.ExchangeContext(ctx, m, r.addr)
	if err != nil {
		return "", err
	}
	for _, rr := range in.Answer {
		if ptr, ok := rr.(*dns.PTR); ok {
			return strings.TrimSuffix(ptr.Ptr, "."), nil
		}
	}
	return "", nil
}
