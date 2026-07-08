package mdnsbridge

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/joshuafuller/beacon/querier"
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

// discoverTimeout bounds each DiscoverServices call. It must be long enough to
// cover the PTR browse phase plus per-instance SRV/TXT/A resolution fallbacks.
const discoverTimeout = 3 * time.Second

// browseInterval is how often each service type is re-browsed after its first
// DiscoverServices call completes.
const browseInterval = 10 * time.Second

// Listener browses mDNS and feeds the Table. It is owned by the host process and
// runs for the process lifetime (independent of engine reloads).
type Listener struct {
	table    *Table
	services []string
	ifaces   []net.Interface
	log      *slog.Logger
}

// NewListener returns a Listener. Empty services uses DefaultServiceTypes; empty
// or ["all"] ifaceNames lets beacon use all multicast interfaces.
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

// Run browses each service type until ctx is canceled, expiring stale entries on
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

// browse repeatedly discovers instances of service until ctx is canceled.
func (l *Listener) browse(ctx context.Context, service string) {
	ticker := time.NewTicker(browseInterval)
	defer ticker.Stop()

	l.discover(ctx, service)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.discover(ctx, service)
		}
	}
}

func (l *Listener) discover(ctx context.Context, service string) {
	q, err := querier.New(l.clientOpts()...)
	if err != nil {
		l.log.Error("mdns: querier init failed", "service", service, "err", err)
		return
	}
	defer func() {
		if closeErr := q.Close(); closeErr != nil {
			l.log.Warn("mdns: querier close failed", "service", service, "err", closeErr)
		}
	}()

	discoverCtx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()

	instances, err := q.DiscoverServices(discoverCtx, service+".local")
	if err != nil {
		l.log.Error("mdns: discover failed", "service", service, "err", err)
		return
	}
	for _, inst := range instances {
		l.ingest(service, inst)
	}
}

func (l *Listener) clientOpts() []querier.Option {
	if len(l.ifaces) == 0 {
		return nil // all interfaces
	}
	return []querier.Option{querier.WithInterfaces(l.ifaces)}
}

// ingest maps a resolved beacon service instance into the Table (naming/publish
// decisions happen in the Table from its config).
func (l *Listener) ingest(service string, inst querier.ServiceInstance) {
	entry := Entry{
		Host:     strings.ToLower(strings.TrimSuffix(inst.Hostname, ".")),
		Service:  service,
		Instance: inst.InstanceName,
		TXT:      inst.TXT,
	}
	if inst.AddrIPv4 != nil {
		if v4 := inst.AddrIPv4.To4(); v4 != nil {
			entry.IPv4 = append(entry.IPv4, v4.String())
		}
	}
	if entry.Host == "" || len(entry.IPv4) == 0 {
		return
	}
	l.table.Upsert(entry)
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
