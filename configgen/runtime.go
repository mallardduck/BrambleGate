package configgen

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeCorefilePath is where the rendered Corefile is written for operator
// inspection (docs/architecture.md). It is NOT the reload mechanism — the engine
// is driven by the in-memory bytes from Render, not this file.
func RuntimeCorefilePath(configDir string) string {
	return filepath.Join(configDir, ".runtime", "Corefile")
}

// ZoneDataPath is where the JSON zone data is written and where the rendered
// Corefile points localrecords. Unlike the Corefile copy, this file IS read by
// the plugin at setup, so it must be written before engine New/Reload.
func ZoneDataPath(configDir string) string {
	return filepath.Join(configDir, ".runtime", "zones", "records.json")
}

// WriteRuntimeCorefile writes a copy of the rendered Corefile under
// <configDir>/.runtime/ so a human can inspect what's actually running.
func WriteRuntimeCorefile(configDir string, corefile []byte) error {
	return writeFileAtomic(RuntimeCorefilePath(configDir), corefile)
}

// WriteZoneData writes the JSON zone data the localrecords plugin loads at setup.
// Write it before calling engine.New/Reload.
func WriteZoneData(configDir string, data []byte) error {
	return writeFileAtomic(ZoneDataPath(configDir), data)
}

// writeFileAtomic writes via a temp file + rename in the same directory, so a
// concurrent reader (e.g. the plugin loading zone data during a reload) never
// sees a half-written file. Same EXDEV rule as store: temp lives beside target.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
