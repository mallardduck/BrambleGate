package mdnsquery

import (
	"context"
	"time"

	"github.com/mallardduck/BrambleGate/mdnssd"
)

// Entry represents a discovered mDNS-SD service instance.
type Entry struct {
	Host     string
	Instance string
	TXT      map[string]string
	IPv4     []string
	IPv6     []string
	// TTL is the record's own announced TTL, as mdnssd last observed it —
	// the real liveness window this device advertised. Zero means unknown
	// (e.g. a FakeBrowser-seeded test entry); callers should treat that as
	// "use a fallback default," not "expired."
	TTL time.Duration
}

// AddFunc is called when a new service instance is discovered.
type AddFunc func(Entry)

// RmvFunc is called when a service instance is removed (goodbye packet or timeout).
type RmvFunc func(Entry)

// TypeFunc is called once per distinct service type discovered by
// BrowseTypes, e.g. "_http._tcp".
type TypeFunc func(serviceType string)

// Browser abstracts mDNS service discovery (browsing).
type Browser interface {
	// Browse blocks until ctx is done, continuously streaming service instance
	// adds and removals for one service type. Entries are filtered by ifaceNames
	// (when non-empty); IPv4/IPv6 are split and populated in the returned Entry.
	// service must be unqualified (e.g. "_http._tcp", not "_http._tcp.local.").
	Browse(ctx context.Context, service string, ifaceNames []string, add AddFunc, rmv RmvFunc) error

	// BrowseTypes blocks until ctx is done, discovering service types
	// actually advertised on the network (via the DNS-SD meta-query) and
	// calling onType once per distinct type found. Used when mdns.services
	// is explicitly set to ["all"], since there's no way to browse
	// "everything" directly — types must be enumerated first.
	BrowseTypes(ctx context.Context, ifaceNames []string, onType TypeFunc) error
}

// New returns a Browser backed by mdnssd. mdnssd replaced brutella/dnssd
// here because dnssd has two gaps that matter for this app: no way to
// browse the DNS-SD meta-query (so service-type discovery beyond a fixed
// list is impossible), and no active cache refresh (RFC 6762 §5.2), so live
// entries were silently evicted on TTL expiry. See mdnssd's doc.go.
func New() Browser {
	return &browser{}
}

type browser struct{}

// Browse implements Browser. Each call opens its own dedicated transport
// scoped to ifaceNames, mirroring the previous dnssd-backed behavior (which
// likewise opened a fresh connection per LookupType call) rather than
// sharing one transport across concurrent Browse calls for different
// service types — mdnssd's Transport.Read is not safe for concurrent
// readers on one underlying connection.
func (b *browser) Browse(ctx context.Context, service string, ifaceNames []string, add AddFunc, rmv RmvFunc) error {
	transport, err := mdnssd.NewUDPTransport(ifaceNames)
	if err != nil {
		return err
	}
	defer func() { _ = transport.Close() }()

	br := mdnssd.New(mdnssd.WithTransport(transport))
	return br.Browse(ctx, service, ifaceNames,
		func(e mdnssd.Entry) { add(toEntry(e)) },
		func(e mdnssd.Entry) { rmv(toEntry(e)) },
	)
}

func toEntry(e mdnssd.Entry) Entry {
	return Entry{Host: e.Host, Instance: e.Instance, TXT: e.TXT, IPv4: e.IPv4, IPv6: e.IPv6, TTL: e.TTL}
}

// BrowseTypes implements Browser. Like Browse, it opens its own dedicated
// transport per call.
func (b *browser) BrowseTypes(ctx context.Context, ifaceNames []string, onType TypeFunc) error {
	transport, err := mdnssd.NewUDPTransport(ifaceNames)
	if err != nil {
		return err
	}
	defer func() { _ = transport.Close() }()

	br := mdnssd.New(mdnssd.WithTransport(transport))
	return br.BrowseTypes(ctx, ifaceNames, func(typ string) { onType(typ) })
}

// FakeBrowser is a test double that synchronously calls add/rmv with canned entries,
// and onType with canned types. Used in unit tests to verify Listener logic without
// real multicast traffic.
type FakeBrowser struct {
	entries []Entry
	types   []string
}

// NewFake returns a FakeBrowser with pre-seeded entries.
func NewFake(entries ...Entry) *FakeBrowser {
	return &FakeBrowser{entries: entries}
}

// NewFakeTypes returns a FakeBrowser whose BrowseTypes synchronously reports
// the given types.
func NewFakeTypes(types ...string) *FakeBrowser {
	return &FakeBrowser{types: types}
}

// WithTypes sets the types a subsequent BrowseTypes call reports, in
// addition to whatever entries were seeded via NewFake. Returns fb for
// chaining, e.g. NewFake(entry).WithTypes("_http._tcp").
func (fb *FakeBrowser) WithTypes(types ...string) *FakeBrowser {
	fb.types = types
	return fb
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

// BrowseTypes implements Browser. It synchronously calls onType once for each
// pre-seeded type. Used only in tests; the ctx is not polled.
func (fb *FakeBrowser) BrowseTypes(ctx context.Context, ifaceNames []string, onType TypeFunc) error {
	for _, t := range fb.types {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			onType(t)
		}
	}
	return nil
}
