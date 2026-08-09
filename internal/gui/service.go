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

	"github.com/mallardduck/BrambleGate/configgen"
	"github.com/mallardduck/BrambleGate/internal/acme"
	"github.com/mallardduck/BrambleGate/internal/mdnsadvertise"
	"github.com/mallardduck/BrambleGate/internal/mdnscfg"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/store"
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
}

// StartAdvertise starts self-advertising this server's own DNS service(s) via
// mDNS-SD, per settings.MDNS.Advertise (independent of settings.MDNS.Enabled,
// which governs discovering OTHER devices). Used both at process startup and
// by SaveSettings' reconcileAdvertise.
func (s *Service) StartAdvertise(settings model.Settings) error {
	advertiser, err := newMDNSAdvertiser(s.baseCtx, s.log)
	if err != nil {
		return fmt.Errorf("start mdns advertiser: %w", err)
	}
	advertiser.Reconcile(settings)
	s.advertiser = advertiser
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
	rendered, err := s.render(settings, rs)
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
	return reloadErr
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
	rendered, err := s.render(settings, rs)
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

func (s *Service) render(settings model.Settings, rs model.RecordSet) (configgen.Rendered, error) {
	rendered, err := configgen.Render(settings, rs, s.certOpts)
	if err != nil {
		return configgen.Rendered{}, ValidationError{err}
	}
	return rendered, nil
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
	_ = configgen.WriteRuntimeCorefile(s.configDir, rendered.Corefile)
	if err := s.reloader.Reload(rendered.Corefile); err != nil {
		return fmt.Errorf("config saved but engine reload failed (previous config still serving): %w", err)
	}
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
