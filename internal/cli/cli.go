// Package cli holds the command-line entrypoint logic for the brambledns binary.
// It lives under internal/ so the extractable library modules (engine, plugins/*)
// can never import it. main.go is a thin shim over Run.
package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mallardduck/BrambleDNS/configgen"
	"github.com/mallardduck/BrambleDNS/engine"
	"github.com/mallardduck/BrambleDNS/store"
)

// Run executes the brambledns command and returns a process exit code.
//
// Phase 2: load settings.yaml + records.yaml via store, render the Corefile via
// configgen (which validates and emits the localrecords records inline), start
// the engine, and shut down gracefully on SIGINT/SIGTERM. The DoT listener still
// uses a throwaway self-signed cert (Phase 4 replaces it). No GUI goroutine yet;
// that lands next, sharing this same process context and *engine.Engine.
func Run(args []string) int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	fs := flag.NewFlagSet("brambledns", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "path to the /config volume root")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := run(log, *configDir); err != nil {
		log.Error("startup failed", "err", err)
		return 1
	}
	return 0
}

func run(log *slog.Logger, configDir string) error {
	st := store.New(configDir)

	settings, err := st.LoadSettings()
	if err != nil {
		return err
	}
	records, err := st.LoadRecords()
	if err != nil {
		return err
	}

	var opts configgen.Options
	if settings.EncryptedListenerEnabled() {
		opts.CertFile, opts.KeyFile, err = ensureSelfSignedCert(configDir, settings.ACME.Domain)
		if err != nil {
			return fmt.Errorf("prepare certificate: %w", err)
		}
		log.Warn("using a self-signed certificate for encrypted listeners — strict clients (e.g. Android Private DNS) will reject it until Phase 4 issues a real ACME cert",
			"cert", opts.CertFile, "cn", settings.ACME.Domain)
	}

	corefile, err := configgen.Render(settings, records, opts)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}

	// Write the rendered Corefile to .runtime/ for operator visibility only — it
	// is not the reload mechanism (docs/architecture.md). Best-effort.
	if err := writeRuntimeCorefile(configDir, corefile); err != nil {
		log.Warn("could not write .runtime/Corefile for inspection", "err", err)
	}

	eng, err := engine.New(corefile)
	if err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	if settings.Listeners.Plain.Enabled {
		log.Info("plain DNS listener up", "port", settings.Listeners.Plain.Port)
	}
	if settings.Listeners.DoT.Enabled {
		log.Info("DoT listener up", "port", settings.Listeners.DoT.Port)
	}
	log.Info("serving local records and forwarding the rest upstream",
		"records", len(records.Records), "upstream", settings.UpstreamDNS.Address)

	// One shared shutdown context for the whole process (the GUI goroutine will
	// select on this same context once it exists).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	log.Info("shutdown signal received, stopping engine")
	if err := eng.Stop(); err != nil {
		return fmt.Errorf("stop engine: %w", err)
	}
	log.Info("stopped cleanly")
	return nil
}

// defaultConfigDir is /config (the mounted volume) unless overridden by
// BRAMBLEDNS_CONFIG_DIR — handy for running outside a container during dev.
func defaultConfigDir() string {
	if d := os.Getenv("BRAMBLEDNS_CONFIG_DIR"); d != "" {
		return d
	}
	return "/config"
}

func writeRuntimeCorefile(configDir string, corefile []byte) error {
	dir := filepath.Join(configDir, ".runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "Corefile"), corefile, 0o644)
}
