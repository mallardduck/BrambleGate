// Package store persists the model to YAML under the /config volume, using the
// atomic write-temp-then-rename pattern (docs/config-schema.md). The user-owned
// files live directly at the config root; generated files live under .runtime/.
//
//	<configDir>/settings.yaml   (user-owned)
//	<configDir>/records.yaml    (user-owned)
//	<configDir>/certs/          (user-owned)
//	<configDir>/.runtime/       (system-owned, regenerated)
package store

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mallardduck/BrambleDNS/model"
)

const (
	settingsFile = "settings.yaml"
	recordsFile  = "records.yaml"
)

// Store reads and writes the config files under a single config directory.
type Store struct {
	dir string
}

// New returns a Store rooted at configDir (the /config volume mount).
func New(configDir string) *Store {
	return &Store{dir: configDir}
}

// Dir is the config root this Store operates on.
func (s *Store) Dir() string { return s.dir }

// SettingsPath and RecordsPath expose the on-disk locations (useful for logs).
func (s *Store) SettingsPath() string { return filepath.Join(s.dir, settingsFile) }
func (s *Store) RecordsPath() string  { return filepath.Join(s.dir, recordsFile) }

// SettingsExist reports whether settings.yaml is already present. A fresh install
// has none; the CLI seeds model.DefaultSettings() on first run (onboarding) so the
// server still comes up working.
func (s *Store) SettingsExist() bool {
	_, err := os.Stat(s.SettingsPath())
	return err == nil
}

// LoadSettings reads and parses <configDir>/settings.yaml.
func (s *Store) LoadSettings() (model.Settings, error) {
	var out model.Settings
	if err := readYAML(s.SettingsPath(), &out); err != nil {
		return out, err
	}
	return out, nil
}

// SaveSettings atomically writes <configDir>/settings.yaml.
func (s *Store) SaveSettings(settings model.Settings) error {
	return writeYAML(s.SettingsPath(), settings)
}

// LoadRecords reads and parses <configDir>/records.yaml. A missing file is
// treated as an empty record set — a fresh install has no records yet.
func (s *Store) LoadRecords() (model.RecordSet, error) {
	var out model.RecordSet
	if _, err := os.Stat(s.RecordsPath()); os.IsNotExist(err) {
		return out, nil
	}
	if err := readYAML(s.RecordsPath(), &out); err != nil {
		return out, err
	}
	return out, nil
}

// SaveRecords atomically writes <configDir>/records.yaml.
func (s *Store) SaveRecords(records model.RecordSet) error {
	return writeYAML(s.RecordsPath(), records)
}

func readYAML(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeYAML(path string, in any) error {
	raw, err := yaml.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return atomicWrite(path, raw, 0o644)
}
