package mdnsquery

import (
	"context"
	"strings"

	"github.com/brutella/dnssd"
)

// Entry represents a discovered mDNS-SD service instance.
type Entry struct {
	Host     string
	Instance string
	TXT      map[string]string
	IPv4     []string
	IPv6     []string
}

// AddFunc is called when a new service instance is discovered.
type AddFunc func(Entry)

// RmvFunc is called when a service instance is removed (goodbye packet or timeout).
type RmvFunc func(Entry)

// Browser abstracts mDNS service discovery (browsing).
type Browser interface {
	// Browse blocks until ctx is done, continuously streaming service instance
	// adds and removals for one service type. Entries are filtered by ifaceNames
	// (when non-empty); IPv4/IPv6 are split and populated in the returned Entry.
	// service must be unqualified (e.g. "_http._tcp", not "_http._tcp.local.").
	Browse(ctx context.Context, service string, ifaceNames []string, add AddFunc, rmv RmvFunc) error
}

// New returns a Browser backed by dnssd.LookupType.
func New() Browser {
	return &browser{}
}

type browser struct{}

// Browse implements Browser.
func (b *browser) Browse(ctx context.Context, service string, ifaceNames []string, add AddFunc, rmv RmvFunc) error {
	// dnssd.LookupType expects the fully-qualified service name with trailing dot.
	fqService := strings.TrimSuffix(service, ".") + ".local."

	// Build a set of allowed interface names for filtering (empty means all).
	allowedIfaces := make(map[string]bool)
	for _, n := range ifaceNames {
		allowedIfaces[n] = true
	}

	return dnssd.LookupType(ctx, fqService,
		func(e dnssd.BrowseEntry) {
			// Filter by interface if specific names were requested.
			if len(allowedIfaces) > 0 && !allowedIfaces[e.IfaceName] {
				return
			}

			// Split mixed v4/v6 IPs from dnssd.BrowseEntry.IPs into separate slices.
			var ipv4, ipv6 []string
			for _, ip := range e.IPs {
				if ip.To4() != nil {
					ipv4 = append(ipv4, ip.String())
				} else {
					ipv6 = append(ipv6, ip.String())
				}
			}

			add(Entry{
				Host:     e.Host,
				Instance: e.Name,
				TXT:      e.Text,
				IPv4:     ipv4,
				IPv6:     ipv6,
			})
		},
		func(e dnssd.BrowseEntry) {
			if len(allowedIfaces) > 0 && !allowedIfaces[e.IfaceName] {
				return
			}

			var ipv4, ipv6 []string
			for _, ip := range e.IPs {
				if ip.To4() != nil {
					ipv4 = append(ipv4, ip.String())
				} else {
					ipv6 = append(ipv6, ip.String())
				}
			}

			rmv(Entry{
				Host:     e.Host,
				Instance: e.Name,
				TXT:      e.Text,
				IPv4:     ipv4,
				IPv6:     ipv6,
			})
		},
	)
}

// FakeBrowser is a test double that synchronously calls add/rmv with canned entries.
// Used in unit tests to verify Listener logic without real multicast traffic.
type FakeBrowser struct {
	entries []Entry
}

// NewFake returns a FakeBrowser with pre-seeded entries.
func NewFake(entries ...Entry) *FakeBrowser {
	return &FakeBrowser{entries: entries}
}

// Browse implements Browser. It synchronously calls add once for each entry, then rmv.
// Used only in tests; the ctx is not polled (all entries are delivered immediately).
func (fb *FakeBrowser) Browse(ctx context.Context, service string, ifaceNames []string, add AddFunc, rmv RmvFunc) error {
	for _, e := range fb.entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			add(e)
		}
	}
	return nil
}
