// Package cli holds the command-line entrypoint logic for the brambledns binary.
// It lives under internal/ so the extractable library modules (engine, plugins/*)
// can never import it. main.go is a thin shim over Run.
package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mallardduck/BrambleDNS/configgen"
	"github.com/mallardduck/BrambleDNS/engine"
	"github.com/mallardduck/BrambleDNS/internal/gui"
	"github.com/mallardduck/BrambleDNS/store"
)

// Run executes the brambledns command and returns a process exit code.
//
// Phase 2: load settings.yaml + records.yaml via store, render the Corefile via
// configgen, start the engine, and start the GUI HTTP server as a second
// goroutine that shares this process's shutdown context and holds the same
// *engine.Engine (so a saved edit re-renders and calls engine.Reload in-process,
// no restart). Both shut down together on SIGINT/SIGTERM (docs/architecture.md).
func Run(args []string) int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	fs := flag.NewFlagSet("brambledns", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "path to the /config volume root")
	guiAddr := fs.String("gui-addr", defaultGUIAddr(), "listen address for the web GUI/API")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := run(log, *configDir, *guiAddr); err != nil {
		log.Error("startup failed", "err", err)
		return 1
	}
	return 0
}

func run(log *slog.Logger, configDir, guiAddr string) error {
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
	// Runtime copy for operator visibility only — not the reload mechanism.
	if err := configgen.WriteRuntimeCorefile(configDir, corefile); err != nil {
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

	// One shared shutdown context for the whole process.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// GUI goroutine: shares ctx and the same *engine.Engine (as the Reloader).
	svc := gui.NewService(st, eng, configDir, opts)
	srv := gui.NewServer(svc, guiAddr)
	go func() {
		log.Info("web GUI up", "addr", guiAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("GUI server failed", "err", err)
			stop() // bring the whole process down if the GUI can't bind
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received, stopping GUI and engine")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("GUI shutdown returned an error", "err", err)
	}
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

// defaultGUIAddr is :8080 unless overridden by BRAMBLEDNS_GUI_ADDR.
func defaultGUIAddr() string {
	if a := os.Getenv("BRAMBLEDNS_GUI_ADDR"); a != "" {
		return a
	}
	return ":8080"
}
