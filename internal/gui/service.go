// Package gui serves the web dashboard and JSON API on its own port, as the
// second process goroutine alongside the engine (docs/architecture.md). It holds
// the same *engine.Engine reference (via the Reloader interface) and the config
// store, and turns a saved edit into store write -> configgen render -> reload,
// all as direct in-process calls — no file-watching, no IPC.
package gui

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mallardduck/BrambleDNS/configgen"
	"github.com/mallardduck/BrambleDNS/internal/mdnscfg"
	"github.com/mallardduck/BrambleDNS/model"
	"github.com/mallardduck/BrambleDNS/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleDNS/store"
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

	mdns *mdnsbridge.Table // nil when mDNS is disabled

	mu sync.Mutex // serializes read-modify-write of the config files
}

// NewService wires the GUI application layer.
func NewService(st *store.Store, reloader Reloader, configDir string, certOpts configgen.Options) *Service {
	return &Service{store: st, reloader: reloader, configDir: configDir, certOpts: certOpts}
}

// SetMDNSTable gives the GUI read/approve access to the live discovery table.
// Pass nil when mDNS is disabled.
func (s *Service) SetMDNSTable(t *mdnsbridge.Table) { s.mdns = t }

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
	reloadErr := s.reload(rendered)
	s.refreshMDNS(settings, rs)
	return reloadErr
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
		if r.NormalizedName() == target && r.Type == rtype {
			return i
		}
	}
	return -1
}
