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

	"github.com/mallardduck/BrambleDNS/engine"
)

// Run executes the brambledns command and returns a process exit code.
//
// Phase 1: load a minimal settings.yaml, ensure a (throwaway, self-signed) cert
// for the DoT listener, render a forward-only Corefile, start the engine, and
// shut it down gracefully on SIGINT/SIGTERM. No GUI goroutine yet — that arrives
// in Phase 2 (see docs/roadmap.md). The engine and GUI will share this same
// process context and *engine.Engine reference.
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
	settings, err := LoadSettings(configDir)
	if err != nil {
		return err
	}

	certFile, keyFile := "", ""
	if settings.Listeners.DoT.Enabled {
		certFile, keyFile, err = ensureSelfSignedCert(configDir, settings.ACME.Domain)
		if err != nil {
			return fmt.Errorf("prepare DoT certificate: %w", err)
		}
		log.Warn("using a self-signed certificate for DoT — strict clients (e.g. Android Private DNS) will reject it until Phase 4 issues a real ACME cert",
			"cert", certFile, "cn", settings.ACME.Domain)
	}

	corefile := renderCorefile(settings, certFile, keyFile)

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
	log.Info("forwarding unmatched queries upstream", "target", settings.forwardTarget())

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
