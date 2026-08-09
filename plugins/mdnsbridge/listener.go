package mdnsbridge

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge/internal/mdnsquery"
)

// DefaultServiceTypes is the curated set of mDNS service types browsed when
// mdns.services is set to the "default" sentinel. Discovering a device under
// any of these yields its hostname, IPs, instance, and TXT — the fields
// selectors match on (docs/plugins.md).
//
// Curation criterion: does this type represent an addressable service worth
// mirroring into DNS for cross-VLAN hostname access, and/or a plausible
// reverse-proxy target? Presence-only/legacy types (_workstation._tcp) and
// types with no stable named service to resolve toward (Matter's mDNS use
// is commissioning-time only; Spotify Connect isn't dialed into by
// hostname) are deliberately left out — mdns.services: [all] covers those
// on demand instead of bloating the default list.
var DefaultServiceTypes = []string{
	"_http._tcp",
	"_https._tcp",
	"_ssh._tcp",
	"_smb._tcp",
	"_ipp._tcp",
	"_ipps._tcp",
	"_printer._tcp",
	"_airplay._tcp",
	"_raop._tcp",
	"_googlecast._tcp",
	"_sonos._tcp",
	"_hap._tcp",
}

// mdns.services has three distinct meanings, not two:
//   - empty: browse nothing (no fixed list, no dynamic discovery).
//   - [defaultServicesSentinel] ("default"): browse DefaultServiceTypes.
//   - [allServicesSentinel] ("all"): discover types dynamically via the
//     DNS-SD meta-query (mdnsquery.Browser.BrowseTypes) instead of a fixed
//     list — there's no way to browse "everything" in one query; RFC
//     6762/6763 requires enumerating types first (see mdnssd's doc.go).
//   - anything else: browse exactly that explicit list.
const (
	defaultServicesSentinel = "default"
	allServicesSentinel     = "all"
)

const expireInterval = 30 * time.Second

// Listener browses mDNS and feeds the Table. It is owned by the host process and
// runs for the process lifetime (independent of engine reloads).
type Listener struct {
	table      *Table
	services   []string
	ifaces     []net.Interface
	ifaceNames []string
	browser    mdnsquery.Browser
	log        *slog.Logger
}

// NewListener returns a Listener. services == [defaultServicesSentinel] uses
// DefaultServiceTypes; services == [allServicesSentinel] discovers types
// dynamically; empty services browses nothing. Empty or ["all"] ifaceNames
// lets the browser use all multicast interfaces.
func NewListener(table *Table, services, ifaceNames []string, log *slog.Logger) *Listener {
	if len(services) == 1 && strings.EqualFold(services[0], defaultServicesSentinel) {
		services = DefaultServiceTypes
	}
	return &Listener{
		table:      table,
		services:   services,
		ifaces:     resolveIfaces(ifaceNames, log),
		ifaceNames: ifaceNames,
		browser:    mdnsquery.New(),
		log:        log,
	}
}

// Run browses each service type until ctx is canceled, expiring stale entries on
// a timer. It never returns an error — mDNS problems are logged, not fatal.
func (l *Listener) Run(ctx context.Context) {
	dynamic := l.usesDynamicServices()
	if dynamic {
		go l.browseAllTypes(ctx)
	} else {
		for _, svc := range l.services {
			go l.browseService(ctx, svc)
		}
	}
	l.log.Info("mdns listener started", "services", len(l.services), "dynamic_services", dynamic, "all_ifaces", len(l.ifaces) == 0)

	ticker := time.NewTicker(expireInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if dropped := l.table.Expire(); dropped > 0 {
				l.log.Debug("mdns: expired stale entries", "count", dropped, "ttl", l.table.ttl)
			}
		}
	}
}

// usesDynamicServices reports whether mdns.services was set to the "all"
// sentinel (and only that), meaning types should be discovered dynamically
// rather than browsed from a fixed list.
func (l *Listener) usesDynamicServices() bool {
	return len(l.services) == 1 && strings.EqualFold(l.services[0], allServicesSentinel)
}

// browseAllTypes discovers service types dynamically via the DNS-SD
// meta-query and starts a browseService goroutine for each newly discovered
// type, deduping so a type re-announcing itself doesn't start a second
// browse. Runs until ctx is canceled.
func (l *Listener) browseAllTypes(ctx context.Context) {
	ifaceNames := l.ifaceNamesForBrowse()
	l.log.Debug("mdns: dynamic type discovery started", "ifaces", ifaceNames)

	var mu sync.Mutex
	started := make(map[string]bool)

	err := l.browser.BrowseTypes(ctx, ifaceNames, func(typ string) {
		mu.Lock()
		if started[typ] {
			mu.Unlock()
			return
		}
		started[typ] = true
		mu.Unlock()

		l.log.Info("mdns: discovered new service type", "type", typ)
		go l.browseService(ctx, typ)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		l.log.Error("mdns: browse types failed", "err", err)
	}
}

// browseService runs a continuous browse for one service type until ctx is canceled.
func (l *Listener) browseService(ctx context.Context, service string) {
	ifaceNames := l.ifaceNamesForBrowse()
	l.log.Debug("mdns: browse started", "service", service, "ifaces", ifaceNames)
	err := l.browser.Browse(ctx, service, ifaceNames,
		func(e mdnsquery.Entry) { l.ingest(service, e) },
		func(e mdnsquery.Entry) { l.remove(service, e) },
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		l.log.Error("mdns: browse failed", "service", service, "err", err)
	}
}

// ifaceNamesForBrowse returns the interface names for filtering browse results.
// Returns nil if all interfaces should be used.
func (l *Listener) ifaceNamesForBrowse() []string {
	if len(l.ifaceNames) == 0 || (len(l.ifaceNames) == 1 && strings.EqualFold(l.ifaceNames[0], "all")) {
		return nil // all interfaces
	}
	return l.ifaceNames
}

// ingest maps a discovered mDNS service instance into the Table (naming/publish
// decisions happen in the Table from its config).
func (l *Listener) ingest(service string, e mdnsquery.Entry) {
	entry := Entry{
		Host:     strings.ToLower(strings.TrimSuffix(e.Host, ".")),
		Service:  service,
		Instance: e.Instance,
		TXT:      e.TXT,
		IPv4:     e.IPv4,
		IPv6:     e.IPv6,
	}
	if entry.Host == "" {
		return
	}
	l.log.Debug("mdns: entry discovered", "service", service, "host", entry.Host,
		"instance", entry.Instance, "ipv4", entry.IPv4, "ipv6", entry.IPv6)
	l.table.Upsert(entry)
}

// remove deletes a service instance from the Table (called on goodbye packet or timeout).
func (l *Listener) remove(service string, e mdnsquery.Entry) {
	host := strings.ToLower(strings.TrimSuffix(e.Host, "."))
	l.log.Debug("mdns: entry removed", "service", service, "host", host, "instance", e.Instance)
	_ = l.table.Remove(service, e.Instance, host)
}

// sanitizeLabel keeps a DNS-safe single label (letters, digits, hyphen).
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func resolveIfaces(names []string, log *slog.Logger) []net.Interface {
	var out []net.Interface
	for _, n := range names {
		if strings.EqualFold(n, "all") || n == "" {
			return nil // explicit "all" → use every multicast-capable interface
		}
		ifi, err := net.InterfaceByName(n)
		if err != nil {
			log.Warn("mdns: configured interface not found, ignoring", "iface", n, "err", err)
			continue
		}
		out = append(out, *ifi)
	}
	return out
}
