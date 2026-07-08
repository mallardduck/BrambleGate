package mdnsbridge

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
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
	table    *Table
	services []string
	ifaces   []net.Interface
	log      *slog.Logger
}

// NewListener returns a Listener. Empty services uses DefaultServiceTypes; empty
// or ["all"] ifaceNames lets zeroconf use all multicast interfaces.
func NewListener(table *Table, services, ifaceNames []string, log *slog.Logger) *Listener {
	if len(services) == 0 {
		services = DefaultServiceTypes
	}
	return &Listener{
		table:    table,
		services: services,
		ifaces:   resolveIfaces(ifaceNames, log),
		log:      log,
	}
}

// Run browses each service type until ctx is cancelled, expiring stale entries on
// a timer. It never returns an error — mDNS problems are logged, not fatal.
func (l *Listener) Run(ctx context.Context) {
	for _, svc := range l.services {
		go l.browse(ctx, svc)
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

func (l *Listener) browse(ctx context.Context, service string) {
	resolver, err := zeroconf.NewResolver(l.clientOpts()...)
	if err != nil {
		l.log.Error("mdns: resolver init failed", "service", service, "err", err)
		return
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	if err := resolver.Browse(ctx, service, "local.", entries); err != nil {
		l.log.Error("mdns: browse failed", "service", service, "err", err)
		return
	}
	// The mainloop closes entries when ctx is cancelled, ending this range.
	for e := range entries {
		l.ingest(service, e)
	}
}

func (l *Listener) clientOpts() []zeroconf.ClientOption {
	if len(l.ifaces) == 0 {
		return nil // all interfaces
	}
	return []zeroconf.ClientOption{zeroconf.SelectIfaces(l.ifaces)}
}

// ingest maps a zeroconf entry into the Table (naming/publish decisions happen in
// the Table from its config).
func (l *Listener) ingest(service string, e *zeroconf.ServiceEntry) {
	entry := Entry{
		Host:     strings.ToLower(e.HostName),
		Service:  service,
		Instance: e.Instance,
		TXT:      parseTXT(e.Text),
	}
	for _, ip := range e.AddrIPv4 {
		if v4 := ip.To4(); v4 != nil {
			entry.IPv4 = append(entry.IPv4, v4.String())
		}
	}
	for _, ip := range e.AddrIPv6 {
		if ip.To4() == nil && ip.IsGlobalUnicast() {
			entry.IPv6 = append(entry.IPv6, ip.String())
		}
	}
	if entry.Host == "" || (len(entry.IPv4) == 0 && len(entry.IPv6) == 0) {
		return
	}
	l.table.Upsert(entry)
}

// parseTXT turns "key=value" TXT strings into a map (keys lower-cased).
func parseTXT(text []string) map[string]string {
	if len(text) == 0 {
		return nil
	}
	m := make(map[string]string, len(text))
	for _, kv := range text {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[strings.ToLower(kv[:i])] = kv[i+1:]
		} else if kv != "" {
			m[strings.ToLower(kv)] = ""
		}
	}
	return m
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
			return nil // explicit "all" → let zeroconf use every interface
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
