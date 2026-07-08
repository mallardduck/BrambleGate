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
	"github.com/mallardduck/BrambleDNS/internal/acme"
	"github.com/mallardduck/BrambleDNS/internal/gui"
	"github.com/mallardduck/BrambleDNS/internal/mdnscfg"
	"github.com/mallardduck/BrambleDNS/model"
	"github.com/mallardduck/BrambleDNS/plugins/mdnsbridge"
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

	// Onboarding: a fresh install has no settings.yaml. Seed a working default
	// (plain DNS forwarding to a public resolver) so the container comes up as a
	// resolving front door immediately; the operator then points upstream_dns at
	// their own ad-block resolver and adds VLANs/records via the GUI or YAML.
	if !st.SettingsExist() {
		if err := st.SaveSettings(model.DefaultSettings()); err != nil {
			return fmt.Errorf("seed default settings.yaml: %w", err)
		}
		log.Warn("no settings.yaml found — wrote a default config; edit upstream_dns to point at your ad-block resolver",
			"path", st.SettingsPath(), "upstream", model.DefaultSettings().UpstreamDNS.Address)
	}

	settings, err := st.LoadSettings()
	if err != nil {
		return err
	}
	records, err := st.LoadRecords()
	if err != nil {
		return err
	}

	opts := configgen.Options{ConfigDir: configDir}
	if settings.EncryptedListenerEnabled() {
		opts.CertFile, opts.KeyFile, err = ensureSelfSignedCert(configDir, settings.ACME.Domain)
		if err != nil {
			return fmt.Errorf("prepare certificate: %w", err)
		}
		log.Warn("using a self-signed certificate for encrypted listeners — strict clients (e.g. Android Private DNS) will reject it until Phase 4 issues a real ACME cert",
			"cert", opts.CertFile, "cn", settings.ACME.Domain)
	}

	// The mDNS discovery table is owned by this process (it must outlive engine
	// reloads) and injected into the plugin before the engine starts. nil when
	// mDNS is disabled — the mdnsbridge stanza is then not rendered.
	var mdnsTable *mdnsbridge.Table
	if settings.MDNS.Enabled {
		mdnsTable = mdnsbridge.NewTable(mdnscfg.Build(settings, records), 0)
		mdnsbridge.SetTable(mdnsTable)
	}

	rendered, err := configgen.Render(settings, records, opts)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}
	// The localrecords plugin reads this at setup — it MUST exist before New.
	if err := configgen.WriteZoneData(configDir, rendered.ZoneData); err != nil {
		return fmt.Errorf("write zone data: %w", err)
	}
	// Runtime Corefile copy for operator visibility only — not the reload mechanism.
	if err := configgen.WriteRuntimeCorefile(configDir, rendered.Corefile); err != nil {
		log.Warn("could not write .runtime/Corefile for inspection", "err", err)
	}

	eng, err := engine.New(rendered.Corefile)
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

	// mDNS listener goroutine: browses the network and feeds the shared table,
	// independent of engine reloads. Owned here, not in the plugin (docs/plugins.md).
	if mdnsTable != nil {
		listener := mdnsbridge.NewListener(mdnsTable, settings.MDNS.ServiceTypes, settings.MDNS.Interfaces, log)
		go listener.Run(ctx)
	}

	// GUI goroutine: shares ctx and the same *engine.Engine (as the Reloader). It
	// also holds the mDNS table (nil when disabled) for the candidates view.
	svc := gui.NewService(st, eng, configDir, opts)
	svc.SetMDNSTable(mdnsTable)
	srv := gui.NewServer(svc, guiAddr)
	go func() {
		log.Info("web GUI up", "addr", guiAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("GUI server failed", "err", err)
			stop() // bring the whole process down if the GUI can't bind
		}
	}()

	// ACME goroutine: obtains/renews the real certificate in the background and
	// reloads the engine in place when it lands. The engine is already serving
	// (with the self-signed bootstrap cert) — a provider/connectivity problem is
	// logged and retried, never fatal. A misconfigured provider disables ACME but
	// leaves the server running on the self-signed cert.
	if settings.ACME.Enabled && settings.EncryptedListenerEnabled() {
		mgr, err := acme.NewManager(acmeConfig(configDir, settings), reloadFn(st, eng, opts), log)
		if err != nil {
			log.Error("acme disabled (config error) — serving with the self-signed certificate", "err", err)
		} else {
			log.Info("acme manager started", "provider", settings.ACME.DNSProvider,
				"production", settings.ACME.Production, "domain", settings.ACME.Domain)
			go mgr.Run(ctx)
		}
	}

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

// acmeConfig maps the persisted ACME settings to the acme package's config.
// Credentials are not included — the provider reads those from the environment.
func acmeConfig(configDir string, s model.Settings) acme.Config {
	return acme.Config{
		ConfigDir:       configDir,
		Domain:          s.ACME.Domain,
		Email:           s.ACME.Email,
		Provider:        s.ACME.DNSProvider,
		Production:      s.ACME.Production,
		CADirectoryURL:  s.ACME.CADirectoryURL,
		RenewBeforeDays: s.ACME.RenewBeforeDays,
	}
}

// reloadFn returns the callback the ACME manager (and any future reloader) uses
// to apply an on-disk change: re-render from the current store and swap the
// engine config in place. Cert paths are unchanged — only the file contents are
// (the renewed cert), which the tls directive re-reads on reload.
func reloadFn(st *store.Store, eng *engine.Engine, opts configgen.Options) func() error {
	return func() error {
		settings, err := st.LoadSettings()
		if err != nil {
			return err
		}
		records, err := st.LoadRecords()
		if err != nil {
			return err
		}
		rendered, err := configgen.Render(settings, records, opts)
		if err != nil {
			return err
		}
		if err := configgen.WriteZoneData(opts.ConfigDir, rendered.ZoneData); err != nil {
			return err
		}
		_ = configgen.WriteRuntimeCorefile(opts.ConfigDir, rendered.Corefile)
		return eng.Reload(rendered.Corefile)
	}
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
