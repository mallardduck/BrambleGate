package mdnsbridge

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge/internal/mdnsquery"
)

// DefaultServiceTypes is the set of mDNS service types browsed when settings.yaml
// lists none. Discovering a device under any of these yields its hostname, IPs,
// instance, and TXT — the fields selectors match on (docs/plugins.md).
var DefaultServiceTypes = []string{
	"_workstation._tcp",
	"_http._tcp",
	"_https._tcp",
	"_ssh._tcp",
	"_smb._tcp",
	"_ipp._tcp",
	"_printer._tcp",
	"_airplay._tcp",
	"_googlecast._tcp",
	"_homekit._tcp",
	"_hap._tcp",
}

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

// NewListener returns a Listener. Empty services uses DefaultServiceTypes; empty
// or ["all"] ifaceNames lets the browser use all multicast interfaces.
func NewListener(table *Table, services, ifaceNames []string, log *slog.Logger) *Listener {
	if len(services) == 0 {
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
	for _, svc := range l.services {
		go l.browseService(ctx, svc)
	}
	l.log.Info("mdns listener started", "services", len(l.services), "all_ifaces", len(l.ifaces) == 0)

	ticker := time.NewTicker(expireInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.table.Expire()
		}
	}
}

// browseService runs a continuous browse for one service type until ctx is canceled.
func (l *Listener) browseService(ctx context.Context, service string) {
	ifaceNames := l.ifaceNamesForBrowse()
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
	l.table.Upsert(entry)
}

// remove deletes a service instance from the Table (called on goodbye packet or timeout).
func (l *Listener) remove(service string, e mdnsquery.Entry) {
	host := strings.ToLower(strings.TrimSuffix(e.Host, "."))
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
			return nil // explicit "all" → let beacon use every interface
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
