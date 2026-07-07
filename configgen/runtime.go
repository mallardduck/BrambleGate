package configgen

import (
	"os"
	"path/filepath"
)

// RuntimeCorefilePath is where the rendered Corefile is written for operator
// inspection (docs/architecture.md). It is NOT the reload mechanism — the engine
// is driven by the in-memory bytes from Render, not this file.
func RuntimeCorefilePath(configDir string) string {
	return filepath.Join(configDir, ".runtime", "Corefile")
}

// WriteRuntimeCorefile writes a copy of the rendered Corefile under
// <configDir>/.runtime/ so a human can inspect what's actually running.
func WriteRuntimeCorefile(configDir string, corefile []byte) error {
	path := RuntimeCorefilePath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, corefile, 0o644)
}
