// Package gui serves the web dashboard and JSON API on its own port, as the
// second process goroutine alongside the engine (docs/architecture.md). It holds
// the same *engine.Engine reference (via the Reloader interface) and the config
// store, and turns a saved edit into store write -> configgen render -> reload,
// all as direct in-process calls — no file-watching, no IPC.
package gui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/mallardduck/BrambleGate/internal/acme"
	"github.com/mallardduck/BrambleGate/internal/configgen"
	"github.com/mallardduck/BrambleGate/internal/configgen/selfip"
	"github.com/mallardduck/BrambleGate/internal/gatewaydetect"
	"github.com/mallardduck/BrambleGate/internal/mdnscfg"
	"github.com/mallardduck/BrambleGate/internal/store"
	"github.com/mallardduck/BrambleGate/internal/vlancfg"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/pluginreg"
	"github.com/mallardduck/BrambleGate/plugins/clientnames"

	// plugins/hosts is registration-only (its init() is what reload's
	// SetLoaded("hosts", ...) call below reports against) — blank-imported
	// here for the same reason internal/engine/directives.go does: gui
	// never calls anything in that package, only pluginreg by name.
	_ "github.com/mallardduck/BrambleGate/plugins/hosts"
	"github.com/mallardduck/BrambleGate/plugins/mdnsadvertise"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/plugins/querylog"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// Reloader is the slice of the engine the GUI needs: a graceful in-process
// config swap. *engine.Engine satisfies it. Kept as an interface so the service
// is testable without binding real DNS sockets.
type Reloader interface {
	Reload(corefile []byte) error
}

// Service is the application layer behind the HTTP handlers. Every mutating
// operation goes through applyRecords/applySettings so validation, persistence,
// runtime-Corefile visibility, and reload always happen together and in order.
type Service struct {
	store     *store.Store
	reloader  Reloader
	configDir string
	certOpts  configgen.Options
	baseCtx   context.Context // parent for the mDNS listener goroutine's lifetime
	log       *slog.Logger

	mdns *mdnsbridge.Table // nil when mDNS is disabled

	mdnsCancel context.CancelFunc // non-nil while the browse goroutine is running
	mdnsCfg    mdnsListenerConfig // services/ifaces the running goroutine was started with

	advertiser mdnsAdvertiser // nil when self-advertisement (mdns.advertise.enabled) is off

	clientNames *clientnames.Table // nil when client_names.enabled is off
	cnCancel    context.CancelFunc // non-nil while clientNames' resolve/sweep worker is running

	mu sync.Mutex // serializes read-modify-write of the config files
}

// mdnsAdvertiser is the slice of *mdnsadvertise.Advertiser the Service needs.
// Kept as an interface so tests can stub it without opening real mDNS
// multicast sockets — the same rationale as the Reloader interface above.
type mdnsAdvertiser interface {
	Reconcile(model.Settings)
	Close() error
}

// newMDNSAdvertiser constructs the real advertiser. Overridable for tests.
var newMDNSAdvertiser = func(ctx context.Context, log *slog.Logger) (mdnsAdvertiser, error) {
	return mdnsadvertise.New(ctx, log)
}

// mdnsListenerConfig is the subset of model.MDNS the running browse goroutine
// was configured with — compared on every SaveSettings to decide whether the
// goroutine needs restarting (Enabled/Suffix/AutoPublish/Naming changes don't
// require a restart; only the actual browse parameters do).
type mdnsListenerConfig struct {
	services []string
	ifaces   []string
}

func (c mdnsListenerConfig) equal(other mdnsListenerConfig) bool {
	return slices.Equal(c.services, other.services) && slices.Equal(c.ifaces, other.ifaces)
}

// runMDNSListener starts browsing and blocks until ctx is canceled. Overridable
// so tests can exercise the enable/disable/reconfigure lifecycle without
// binding real mDNS multicast sockets.
var runMDNSListener = func(ctx context.Context, tbl *mdnsbridge.Table, services, ifaces []string, log *slog.Logger) {
	mdnsbridge.NewListener(tbl, services, ifaces, log).Run(ctx)
}

// detectSelfIPs computes fresh per-VLAN local IPs on every render — interfaces
// can change across a macvlan reattach/restart, so this is never cached.
// Overridable for tests (same pattern as newMDNSAdvertiser/runMDNSListener
// above).
var detectSelfIPs = selfip.DetectLive

// detectVLANCandidates finds locally-attached networks not yet covered by any
// declared VLAN, for the Settings page's "detected networks" suggestions.
// Same never-cached, overridable-for-tests treatment as detectSelfIPs.
var detectVLANCandidates = selfip.CandidatesLive

// detectGateways guesses each VLAN's gateway router IP, for the
// clientnames PTR tier's default target when client_names.ptr_upstream
// isn't set (dev-docs/client-names.md). Same never-cached,
// overridable-for-tests treatment as detectSelfIPs.
var detectGateways = gatewaydetect.DetectLive

// NewService wires the GUI application layer. ctx bounds the lifetime of the
// mDNS browse goroutine (started/stopped independently of engine reloads as
// mdns.enabled is toggled); log is used only for that goroutine's own logging.
func NewService(ctx context.Context, st *store.Store, reloader Reloader, configDir string, certOpts configgen.Options, log *slog.Logger) *Service {
	return &Service{store: st, reloader: reloader, configDir: configDir, certOpts: certOpts, baseCtx: ctx, log: log}
}

// SetMDNSTable gives the GUI read/approve access to a discovery table without
// starting a browse goroutine — used by callers (and tests) that manage the
// table's lifecycle themselves. Pass nil when mDNS is disabled. For the normal
// "let the GUI own the whole lifecycle" path, mDNS starts/stops/reconfigures
// itself from SaveSettings; see StartMDNS for adopting an already-built table
// at process startup.
func (s *Service) SetMDNSTable(t *mdnsbridge.Table) { s.mdns = t }

// StartMDNS adopts tbl (already injected into the mdnsbridge plugin via
// mdnsbridge.SetTable by the caller) and starts its browse goroutine, bound to
// the Service's ctx. Used once at process startup when mDNS is enabled from the
// start; later enable/disable/reconfigure transitions go through SaveSettings.
func (s *Service) StartMDNS(tbl *mdnsbridge.Table, cfg model.MDNS) {
	s.mdns = tbl
	ctx, cancel := context.WithCancel(s.baseCtx)
	s.mdnsCancel = cancel
	s.mdnsCfg = mdnsListenerConfig{services: cfg.ServiceTypes, ifaces: cfg.Interfaces}
	go runMDNSListener(ctx, tbl, cfg.ServiceTypes, cfg.Interfaces, s.log)
}

// StopMDNS cancels the browse goroutine (if running) and clears the table, both
// locally and from the plugin's injected global.
func (s *Service) StopMDNS() {
	if s.mdnsCancel != nil {
		s.mdnsCancel()
		s.mdnsCancel = nil
	}
	s.mdnsCfg = mdnsListenerConfig{}
	if s.mdns != nil {
		mdnsbridge.SetTable(nil)
		s.mdns = nil
	}
	pluginreg.SetLoaded("mdnsbridge", false, "disabled in settings")
}

// StartAdvertise starts self-advertising this server's own DNS service(s) via
// mDNS-SD, per settings.MDNS.Advertise (independent of settings.MDNS.Enabled,
// which governs discovering OTHER devices). Used both at process startup and
// by SaveSettings' reconcileAdvertise.
func (s *Service) StartAdvertise(settings model.Settings) error {
	advertiser, err := newMDNSAdvertiser(s.baseCtx, s.log)
	if err != nil {
		pluginreg.SetLoaded("mdnsadvertise", false, "failed to start: "+err.Error())
		return fmt.Errorf("start mdns advertiser: %w", err)
	}
	advertiser.Reconcile(settings)
	s.advertiser = advertiser
	pluginreg.SetLoaded("mdnsadvertise", true, "")
	return nil
}

// StopAdvertise closes the advertiser (if running), sending goodbye packets
// for everything it had registered.
func (s *Service) StopAdvertise() {
	if s.advertiser != nil {
		if err := s.advertiser.Close(); err != nil {
			s.log.Error("mdns advertise: close failed", "err", err)
		}
		s.advertiser = nil
	}
	pluginreg.SetLoaded("mdnsadvertise", false, "disabled in settings")
}

// reconcileAdvertise starts/stops/reconfigures self-advertisement to match the
// just-saved settings. Called at the end of every SaveSettings, independent of
// whatever else changed — catches e.g. a DoT-enabled edit even when
// mdns.advertise.enabled itself didn't change.
func (s *Service) reconcileAdvertise(settings model.Settings) {
	switch {
	case !settings.MDNS.Advertise.Enabled:
		s.StopAdvertise()
	case s.advertiser == nil:
		if err := s.StartAdvertise(settings); err != nil {
			s.log.Error("mdns advertise disabled (start error)", "err", err)
		}
	default:
		s.advertiser.Reconcile(settings)
	}
}

// reconcileQueryLogStore opens/tunes/closes the durable Query Log store to
// match the just-saved settings. Called at the end of every SaveSettings,
// independent of whatever else changed — same shape as reconcileAdvertise,
// and for the same reason: Store owns a real external resource whose
// lifecycle must track the full settings on every save, not just whether
// the "querylog" Corefile stanza happens to be present (only setup() sees
// that, and setup() never runs at all once Query Log is disabled — see
// dev-docs/query-log.md's Phase 7b and querylog.ReconcileStore's doc
// comment).
func (s *Service) reconcileQueryLogStore(settings model.Settings) {
	if err := querylog.ReconcileStore(configgen.QueryLogStoreConfig(settings.QueryLog, s.configDir)); err != nil {
		s.log.Error("querylog store reconcile failed", "err", err)
	}
}

// clientNamesConfig builds a clientnames.Config from the current settings +
// hosts.yaml + whatever mDNS table (if any) is currently running — the
// single choke point StartClientNames/refreshClientNames/reconcileClientNames
// all go through so the Table's live tier-0/tier-1 sources never drift from
// what SaveSettings/AddHost et al. actually persisted.
//
// PTR targets default to each VLAN's auto-detected gateway router
// (detectGateways), not upstream_dns — the general ad-block resolver
// usually has no idea what's on the LAN, but the router almost always
// both knows local reverse names and doubles as the LAN's DNS server,
// the same assumption Pi-hole's own client-name resolution leans on.
// client_names.ptr_upstream, when set, is an explicit override that wins
// for every VLAN instead (dev-docs/client-names.md).
func (s *Service) clientNamesConfig(settings model.Settings, hs model.HostSet) clientnames.Config {
	resolvers := map[string]clientnames.Resolver{}
	var unmatched clientnames.Resolver
	if settings.ClientNames.PTRUpstream != "" {
		r := clientnames.NewPTRResolver(settings.ClientNames.PTRUpstream)
		unmatched = r
		for _, v := range settings.VLANs {
			resolvers[v.Name] = r
		}
	} else {
		gw := detectGateways(settings.VLANs)
		for name, ip := range gw.PerVLAN {
			resolvers[name] = clientnames.NewPTRResolver(ip + ":53")
		}
		if gw.Primary != "" {
			unmatched = clientnames.NewPTRResolver(gw.Primary + ":53")
		}
	}

	idx := make(map[string]string, len(hs.Hosts))
	for _, h := range hs.Hosts {
		idx[h.IP] = h.Hostname
	}
	return clientnames.Config{
		HostsIndex:        idx,
		MDNS:              s.mdns,
		Resolvers:         resolvers,
		UnmatchedResolver: unmatched,
		RefreshHostnames:  settings.ClientNames.RefreshHostnames,
	}
}

// StartClientNames creates the client-name Table, starts its background
// resolve/sweep worker bound to the Service's ctx, and registers it as
// querylog's passive client-IP observer (dev-docs/client-names.md). Used both
// at process startup (when client_names.enabled is already true) and by
// reconcileClientNames on a later settings change.
func (s *Service) StartClientNames(settings model.Settings, hs model.HostSet) {
	tbl := clientnames.NewTable(s.clientNamesConfig(settings, hs))
	ctx, cancel := context.WithCancel(s.baseCtx)
	s.cnCancel = cancel
	s.clientNames = tbl
	go tbl.Run(ctx)
	querylog.SetClientObserver(tbl.Observe)
	pluginreg.SetLoaded("clientnames", true, "")
}

// StopClientNames cancels the resolve/sweep worker (if running), unregisters
// the querylog observer, and drops the Table.
func (s *Service) StopClientNames() {
	if s.cnCancel != nil {
		s.cnCancel()
		s.cnCancel = nil
	}
	if s.clientNames != nil {
		querylog.SetClientObserver(nil)
		s.clientNames = nil
	}
	pluginreg.SetLoaded("clientnames", false, "disabled in settings")
}

// refreshClientNames pushes the latest hosts/mDNS/PTR config into the
// already-running Table — called after any hosts.yaml change (applyHosts)
// so tier 0 never serves a stale IP->name mapping. A no-op when client names
// are disabled.
func (s *Service) refreshClientNames(settings model.Settings, hs model.HostSet) {
	if s.clientNames != nil {
		s.clientNames.SetConfig(s.clientNamesConfig(settings, hs))
	}
}

// reconcileClientNames starts/stops/reconfigures client-name resolution to
// match the just-saved settings, called at the end of every SaveSettings —
// same shape as reconcileAdvertise, and deliberately run after
// mdnsPostReload so s.mdns already reflects any mDNS enable/disable from the
// same save.
func (s *Service) reconcileClientNames(settings model.Settings, hs model.HostSet) {
	switch {
	case !settings.ClientNames.Enabled:
		s.StopClientNames()
	case s.clientNames == nil:
		s.StartClientNames(settings, hs)
	default:
		s.refreshClientNames(settings, hs)
		pluginreg.SetLoaded("clientnames", true, "")
	}
}

// ErrClientNamesDisabled is returned by the /api/clients endpoint when
// client name resolution is off.
var ErrClientNamesDisabled = ValidationError{errors.New("client name resolution is disabled (set client_names.enabled: true)")}

// Clients returns the current client-name cache for the GUI/API.
func (s *Service) Clients() ([]clientnames.Entry, error) {
	if s.clientNames == nil {
		return nil, ErrClientNamesDisabled
	}
	return s.clientNames.Snapshot(), nil
}

// ErrMDNSDisabled is returned by the mDNS endpoints when discovery is off.
var ErrMDNSDisabled = ValidationError{errors.New("mDNS is disabled (set mdns.enabled: true)")}

// MDNSCandidates returns the current discovery table for the GUI.
func (s *Service) MDNSCandidates() ([]mdnsbridge.Entry, error) {
	if s.mdns == nil {
		return nil, ErrMDNSDisabled
	}
	return s.mdns.Snapshot(), nil
}

// SetMDNSPublished approves/unapproves a discovered entry for serving (runtime
// state only — no records.yaml write).
func (s *Service) SetMDNSPublished(name string, published bool) error {
	if s.mdns == nil {
		return ErrMDNSDisabled
	}
	if !s.mdns.SetPublished(name, published) {
		return ErrNotFound
	}
	return nil
}

// PromoteMDNS turns a discovered entry into a durable *live* record in
// records.yaml: a type:mdns record whose value is resolved from the discovery
// table via a selector (keyed on the device's host, the stable identifier). It
// survives restarts and IP changes, and answers empty (NODATA) when the device is
// absent — it is NOT a frozen snapshot of the current IP (docs/plugins.md).
func (s *Service) PromoteMDNS(name string) error {
	if s.mdns == nil {
		return ErrMDNSDisabled
	}
	var entry mdnsbridge.Entry
	for _, e := range s.mdns.Snapshot() {
		if strings.EqualFold(e.Name, ensureDot(name)) {
			entry = e
			break
		}
	}
	if entry.Name == "" {
		return ErrNotFound
	}

	host := strings.TrimSuffix(entry.Host, ".")
	if host == "" {
		return ValidationError{fmt.Errorf("discovered entry %q has no host to link", name)}
	}
	rec := model.Record{
		Name:  strings.TrimSuffix(entry.Name, "."),
		Type:  model.TypeMDNS,
		Match: &model.Selector{Host: host},
	}
	// AddRecord runs the normal validate→save→reload pipeline and (via
	// applyRecords) refreshes the discovery table's promoted bindings.
	return s.AddRecord(rec)
}

func ensureDot(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

// refreshMDNS rebuilds the discovery table's config from the current settings +
// records (promoted bindings, auto-publish, naming). Called after any save so a
// new type:mdns record or an mdns settings change takes effect live.
func (s *Service) refreshMDNS(settings model.Settings, rs model.RecordSet) {
	if s.mdns != nil {
		s.mdns.SetConfig(mdnscfg.Build(settings, rs))
	}
}

// ValidationError marks an error caused by invalid user input (renders to HTTP
// 400) as opposed to an internal failure. Reload failures are NOT validation
// errors — the config was persisted but could not be applied.
type ValidationError struct{ err error }

func (e ValidationError) Error() string { return e.err.Error() }
func (e ValidationError) Unwrap() error { return e.err }

// IsValidation reports whether err originated from model validation.
func IsValidation(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

// Records returns the current record set.
func (s *Service) Records() (model.RecordSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.LoadRecords()
}

// Settings returns the current settings.
func (s *Service) Settings() (model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.LoadSettings()
}

// ACMEStatus reads the on-disk cert under <configDir>/certs for display (e.g.
// the dashboard) — independent of ACME being enabled, since a self-signed
// placeholder is also worth showing.
func (s *Service) ACMEStatus() acme.Status {
	return acme.ReadStatus(s.configDir)
}

// SaveRecords validates+persists the record set and reloads the engine.
func (s *Service) SaveRecords(rs model.RecordSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	return s.applyRecords(settings, rs)
}

// SaveSettings validates+persists settings and reloads the engine. Records are
// re-read so the rendered Corefile stays consistent with them.
//
// mDNS enable/disable/reconfigure is the one transition that must straddle the
// reload: turning it on requires the table to exist and be injected into the
// mdnsbridge plugin (mdnsPreReload) BEFORE the reload that adds the `mdnsbridge`
// stanza to the Corefile (the plugin's setup() reads the injected table
// synchronously while parsing); turning it off, or (re)starting the browse
// goroutine, is safe only AFTER the reload has swapped the stanza in/out
// (mdnsPostReload).
func (s *Service) SaveSettings(settings model.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.store.LoadRecords()
	if err != nil {
		return err
	}
	hs, err := s.store.LoadHosts()
	if err != nil {
		return err
	}
	rendered, err := s.render(settings, rs, hs)
	if err != nil {
		return err
	}
	if err := s.store.SaveSettings(settings); err != nil {
		return err
	}
	s.mdnsPreReload(settings, rs)
	reloadErr := s.reload(rendered)
	s.mdnsPostReload(settings, rs)
	s.reconcileAdvertise(settings)
	s.reconcileQueryLogStore(settings)
	s.reconcileClientNames(settings, hs)
	return reloadErr
}

// RestartEngine re-renders the current on-disk settings/records/hosts and
// forces a fresh in-process engine reload, without changing any persisted
// state. Backs the Settings page's "Restart DNS engine" admin action — a way
// to force-apply the on-disk config (e.g. after it drifted from what's
// actually running) without having to touch a field and re-save.
func (s *Service) RestartEngine() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	rs, err := s.store.LoadRecords()
	if err != nil {
		return err
	}
	hs, err := s.store.LoadHosts()
	if err != nil {
		return err
	}
	rendered, err := s.render(settings, rs, hs)
	if err != nil {
		return err
	}
	return s.reload(rendered)
}

// mdnsPreReload creates and injects the discovery table when mDNS is being
// turned on, so the about-to-run reload's Corefile parse finds it. It does not
// start the browse goroutine — that happens in mdnsPostReload, once the reload
// (and therefore the new Corefile) has actually landed.
func (s *Service) mdnsPreReload(settings model.Settings, rs model.RecordSet) {
	if settings.MDNS.Enabled && s.mdns == nil {
		tbl := mdnsbridge.NewTable(mdnscfg.Build(settings, rs), 0)
		mdnsbridge.SetTable(tbl)
		s.mdns = tbl
	}
}

// mdnsPostReload starts/stops/restarts the browse goroutine to match the new
// settings, now that the reload has applied (or failed to apply — either way,
// the Corefile the running engine now serves reflects the persisted settings).
func (s *Service) mdnsPostReload(settings model.Settings, rs model.RecordSet) {
	switch {
	case !settings.MDNS.Enabled:
		s.StopMDNS()
	case s.mdnsCancel == nil:
		s.StartMDNS(s.mdns, settings.MDNS)
	case !s.mdnsCfg.equal(mdnsListenerConfig{services: settings.MDNS.ServiceTypes, ifaces: settings.MDNS.Interfaces}):
		s.mdnsCancel()
		// Entries discovered under the old service-type/interface filter
		// won't be refreshed by the new browse config, and would otherwise
		// sit in the table (and /mdns candidates) until their TTL expires
		// instead of reflecting the change immediately.
		s.mdns.Clear()
		s.StartMDNS(s.mdns, settings.MDNS)
	default:
		s.refreshMDNS(settings, rs)
	}
}

// AddRecord appends a record, rejecting a duplicate (same name+type).
func (s *Service) AddRecord(r model.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	rs, err := s.store.LoadRecords()
	if err != nil {
		return err
	}
	if idx := indexOf(rs, r.Name, r.Type); idx >= 0 {
		return ValidationError{fmt.Errorf("a %s record for %q already exists", r.Type, r.Name)}
	}
	rs.Records = append(rs.Records, r)
	return s.applyRecords(settings, rs)
}

// UpdateRecord replaces the record identified by name+type; 404-style error if
// absent.
func (s *Service) UpdateRecord(name string, rtype model.RecordType, r model.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	rs, err := s.store.LoadRecords()
	if err != nil {
		return err
	}
	idx := indexOf(rs, name, rtype)
	if idx < 0 {
		return ErrNotFound
	}
	rs.Records[idx] = r
	return s.applyRecords(settings, rs)
}

// DeleteRecord removes the record identified by name+type; 404-style if absent.
func (s *Service) DeleteRecord(name string, rtype model.RecordType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	rs, err := s.store.LoadRecords()
	if err != nil {
		return err
	}
	idx := indexOf(rs, name, rtype)
	if idx < 0 {
		return ErrNotFound
	}
	rs.Records = append(rs.Records[:idx], rs.Records[idx+1:]...)
	return s.applyRecords(settings, rs)
}

// ErrNotFound is returned when addressing a record that does not exist.
var ErrNotFound = errors.New("record not found")

// applyRecords renders (validating), then persists, then reloads. Validation
// runs BEFORE the write so an invalid edit never touches the YAML on disk.
func (s *Service) applyRecords(settings model.Settings, rs model.RecordSet) error {
	hs, err := s.store.LoadHosts()
	if err != nil {
		return err
	}
	rendered, err := s.render(settings, rs, hs)
	if err != nil {
		return err
	}
	if err := s.store.SaveRecords(rs); err != nil {
		return err
	}
	reloadErr := s.reload(rendered)
	s.refreshMDNS(settings, rs)
	return reloadErr
}

// Hosts returns the current host set.
func (s *Service) Hosts() (model.HostSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.LoadHosts()
}

// AddHost appends a host entry, rejecting a duplicate name across
// hostname+aliases project-wide (mirrors AddRecord's duplicate check;
// configgen.Validate would also catch this at render time, but rejecting it
// here gives a clearer, field-specific error before any write happens).
// AddHost's, UpdateHost's, and DeleteHost's warnings return value is
// configgen.Rendered.Warnings from the render this call triggered (e.g. a
// hosts.yaml name shadowing a records.yaml one) — non-fatal, but worth
// surfacing to whoever just made the edit (dev-docs/static-hosts.md's
// "precedence consequence" note); nil on a real error, since nothing was
// rendered.
func (s *Service) AddHost(h model.Host) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	hs, err := s.store.LoadHosts()
	if err != nil {
		return nil, err
	}
	for _, name := range h.Names() {
		if idx := indexOfHostName(hs, name); idx >= 0 {
			return nil, ValidationError{fmt.Errorf("a hosts entry for %q already exists", name)}
		}
	}
	hs.Hosts = append(hs.Hosts, h)
	return s.applyHosts(settings, hs)
}

// UpdateHost replaces the host entry identified by its current hostname;
// 404-style error if absent.
func (s *Service) UpdateHost(hostname string, h model.Host) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	hs, err := s.store.LoadHosts()
	if err != nil {
		return nil, err
	}
	idx := indexOfHost(hs, hostname)
	if idx < 0 {
		return nil, ErrNotFound
	}
	hs.Hosts[idx] = h
	return s.applyHosts(settings, hs)
}

// DeleteHost removes the host entry identified by its hostname; 404-style
// if absent.
func (s *Service) DeleteHost(hostname string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	hs, err := s.store.LoadHosts()
	if err != nil {
		return nil, err
	}
	idx := indexOfHost(hs, hostname)
	if idx < 0 {
		return nil, ErrNotFound
	}
	hs.Hosts = append(hs.Hosts[:idx], hs.Hosts[idx+1:]...)
	return s.applyHosts(settings, hs)
}

// applyHosts mirrors applyRecords: render (validating) before persisting,
// then reload. Hosts never affect mDNS, so there's no refreshMDNS
// counterpart here.
func (s *Service) applyHosts(settings model.Settings, hs model.HostSet) ([]string, error) {
	rs, err := s.store.LoadRecords()
	if err != nil {
		return nil, err
	}
	rendered, err := s.render(settings, rs, hs)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveHosts(hs); err != nil {
		return nil, err
	}
	s.refreshClientNames(settings, hs)
	return rendered.Warnings, s.reload(rendered)
}

func indexOfHost(hs model.HostSet, hostname string) int {
	target := model.NormalizedHostName(hostname)
	for i, h := range hs.Hosts {
		if model.NormalizedHostName(h.Hostname) == target {
			return i
		}
	}
	return -1
}

// indexOfHostName searches across every host's full name set (hostname +
// aliases), unlike indexOfHost which only matches the canonical hostname —
// used for AddHost's project-wide duplicate check (dev-docs/static-hosts.md:
// dedup applies across hostname and aliases alike).
func indexOfHostName(hs model.HostSet, name string) int {
	target := model.NormalizedHostName(name)
	for i, h := range hs.Hosts {
		for _, n := range h.Names() {
			if model.NormalizedHostName(n) == target {
				return i
			}
		}
	}
	return -1
}

func (s *Service) render(settings model.Settings, rs model.RecordSet, hs model.HostSet) (configgen.Rendered, error) {
	// The process-wide configured-VLANs table localrecords/mdnsbridge read as
	// their source of truth at request time. render is the single choke
	// point every settings/records/hosts change (SaveSettings, Add/Update/Delete
	// Record, Add/Update/Delete Host, mDNS promotion) passes through, so this
	// is the one place the GUI side needs to refresh it (dev-docs/query-log.md).
	vlanmatch.SetCurrent(vlanmatch.NewTable(vlancfg.Build(settings.VLANs)))
	opts := s.certOpts
	opts.ACMESelfIPs = detectSelfIPs(settings.VLANs)
	rendered, err := configgen.Render(settings, rs, hs, opts)
	if err != nil {
		return configgen.Rendered{}, ValidationError{err}
	}
	return rendered, nil
}

// ACMESelfRecords returns the record(s) that would be auto-answered for
// acme.domain from local-IP detection right now — for display only (the
// dashboard's "auto-detected address" panel); never persisted to
// records.yaml (see configgen.PreviewACMESelfRecords).
func (s *Service) ACMESelfRecords() ([]model.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	rs, err := s.store.LoadRecords()
	if err != nil {
		return nil, err
	}
	return configgen.PreviewACMESelfRecords(settings, rs.Records, detectSelfIPs(settings.VLANs)), nil
}

// VLANCandidates returns locally-attached networks not yet covered by any
// declared VLAN — suggestions for the Settings page's "detected networks"
// panel. BrambleGate never declares these on its own; the user names and adds
// them (same POST /settings/vlans path as a manually-entered VLAN).
func (s *Service) VLANCandidates() ([]selfip.Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	return detectVLANCandidates(settings.VLANs), nil
}

// reload writes the JSON zone data (which the plugin reads at setup) and a
// runtime Corefile copy for visibility, then performs the graceful engine swap.
// A reload failure means the new config was persisted but the engine kept serving
// the previous config — surfaced to the caller. The zone data is written first so
// the reloaded plugin sees the new records.
func (s *Service) reload(rendered configgen.Rendered) error {
	if err := configgen.WriteZoneData(s.configDir, rendered.ZoneData); err != nil {
		return fmt.Errorf("write zone data: %w", err)
	}
	if err := configgen.WriteHostsData(s.configDir, rendered.HostsData); err != nil {
		return fmt.Errorf("write hosts data: %w", err)
	}
	_ = configgen.WriteRuntimeCorefile(s.configDir, rendered.Corefile)
	if err := s.reloader.Reload(rendered.Corefile); err != nil {
		return fmt.Errorf("config saved but engine reload failed (previous config still serving): %w", err)
	}
	// plugins/hosts has no setup() of its own to call this from — the hosts
	// stanza is unconditionally present in every rendered Corefile, so
	// "loaded" just tracks a successful reload, same as internal/cli's
	// matching calls (run/reloadFn) for the CLI-only startup path.
	pluginreg.SetLoaded("hosts", true, "")
	return nil
}

func indexOf(rs model.RecordSet, name string, rtype model.RecordType) int {
	target := model.Record{Name: name}.NormalizedName()
	for i, r := range rs.Records {
		if r.NormalizedName() == target && strings.EqualFold(string(r.Type), string(rtype)) {
			return i
		}
	}
	return -1
}
