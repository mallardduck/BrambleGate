// Package cli holds the command-line entrypoint logic for the bramblegate binary.
// It lives under internal/ so the extractable library modules (engine, plugins/*)
// can never import it. main.go is a thin shim over Run.
package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mallardduck/BrambleGate/configgen"
	"github.com/mallardduck/BrambleGate/engine"
	"github.com/mallardduck/BrambleGate/internal/acme"
	"github.com/mallardduck/BrambleGate/internal/gui"
	"github.com/mallardduck/BrambleGate/internal/mdnscfg"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/pluginreg"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/selfip"
	"github.com/mallardduck/BrambleGate/store"
)

// Run executes the bramblegate command and returns a process exit code.
//
// Phase 2: load settings.yaml + records.yaml via store, render the Corefile via
// configgen, start the engine, and start the GUI HTTP server as a second
// goroutine that shares this process's shutdown context and holds the same
// *engine.Engine (so a saved edit re-renders and calls engine.Reload in-process,
// no restart). Both shut down together on SIGINT/SIGTERM (docs/architecture.md).
func Run(args []string) int {
	fs := flag.NewFlagSet("bramblegate", flag.ContinueOnError)
	configDir := fs.String("config-dir", defaultConfigDir(), "path to the /config volume root")
	guiAddr := fs.String("gui-addr", defaultGUIAddr(), "listen address for the web GUI/API")
	logLevel := fs.String("log-level", defaultLogLevel(), "log level: debug, info, warn, error")
	logFormat := fs.String("log-format", defaultLogFormat(), "log format: json, text")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	log, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bramblegate:", err)
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
	log.Info("loading config", "dir", configDir, "settings", st.SettingsPath(), "records", st.RecordsPath())

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

	// Cert paths are always populated, regardless of whether an encrypted
	// listener happens to be on yet at boot — they're the same fixed
	// certs/cert.pem, certs/key.pem path acme.Manager writes to, and GUI-driven
	// settings saves reuse this same opts for the rest of the process's life
	// (see NewService below and reloadFn). Gating this on
	// EncryptedListenerEnabled() left opts.CertFile/KeyFile permanently blank
	// whenever ACME issued its cert before any listener was turned on — the
	// documented deferred-issuance path just below — so enabling DoT/DoH/DoQ
	// later via the GUI rendered a `tls` directive with blank args and CoreDNS
	// rejected it.
	opts := configgen.Options{ConfigDir: configDir, ACMESelfIPs: selfip.DetectLive(settings.VLANs)}
	switch {
	case settings.ACME.Enabled:
		// A self-signed placeholder is still written here even though ACME is
		// on: it's the bridge that keeps an encrypted listener bindable at
		// boot while the Manager issues a real cert in the background (see
		// the deferred-issuance comment below) — not the user-facing
		// self-signed *fallback* setting, which only applies when ACME is
		// off (see the case below).
		opts.CertFile, opts.KeyFile, err = ensureSelfSignedCert(configDir, settings.ACME.Domain)
		if err != nil {
			return fmt.Errorf("prepare certificate: %w", err)
		}
	case settings.ACME.SelfSignedFallback:
		opts.CertFile, opts.KeyFile, err = ensureSelfSignedCert(configDir, settings.ACME.Domain)
		if err != nil {
			return fmt.Errorf("prepare certificate: %w", err)
		}
		if settings.EncryptedListenerEnabled() {
			log.Warn("using a self-signed certificate for encrypted listeners (acme.self_signed_fallback) — strict clients (e.g. Android Private DNS) will reject it until ACME is configured",
				"cert", opts.CertFile, "cn", settings.ACME.Domain)
		}
	default:
		// Neither ACME nor the self-signed fallback is on: no cert file
		// exists (or will be created). The fixed path is still populated so
		// a `tls` directive renders correctly the instant either gets turned
		// on later via the GUI — reloadFn never changes these paths, only
		// the file contents at them.
		opts.CertFile, opts.KeyFile = certPaths(configDir)
		if settings.EncryptedListenerEnabled() {
			log.Warn("an encrypted listener is enabled but no certificate is configured — it will not start until acme.enabled or acme.self_signed_fallback is turned on in Settings",
				"cert", opts.CertFile)
		}
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

	// engine.New has just run every CoreDNS-chain plugin's setup() against the
	// rendered Corefile, so Required plugins (localrecords) have had their
	// chance to report Loaded via pluginreg. A failure here means a
	// Corefile-generation bug omitted a structurally required plugin, not a
	// user setting — fail loudly at startup rather than silently serving a
	// broken zone (dev-docs/plugin-system.md).
	if err := pluginreg.Validate(); err != nil {
		return fmt.Errorf("plugin registry validation failed: %w", err)
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

	// GUI goroutine: shares ctx and the same *engine.Engine (as the Reloader). It
	// also owns the mDNS browse goroutine's lifecycle (start now if enabled from
	// boot; later enable/disable/reconfigure happens through Service.SaveSettings).
	svc := gui.NewService(ctx, st, eng, configDir, opts, log)
	if mdnsTable != nil {
		svc.StartMDNS(mdnsTable, settings.MDNS)
	}
	if settings.MDNS.Advertise.Enabled {
		if err := svc.StartAdvertise(settings); err != nil {
			log.Error("mdns advertise disabled (startup error)", "err", err)
		}
	}
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
	// logged and retried, never fatal. A misconfigured provider just skips that
	// reconcile tick, leaving the server on whatever cert is already in place.
	//
	// Started unconditionally, regardless of acme.enabled/settings at this exact
	// instant: the Manager re-reads settings.yaml on every reconcile (loadCfg
	// below), so enabling ACME, flipping acme.production, or changing the
	// domain/provider via the GUI later takes effect on the Manager's next tick
	// — it must not require a process restart to notice a settings.yaml change.
	//
	// Also deliberately not gated on an encrypted listener being on: issuance can
	// be verified (e.g. against staging) before flipping dot/doh/doq on, so the
	// cert is ready and trusted by the time a listener needs it.
	mgr, err := acme.NewManager(func() (acme.Config, error) {
		s, err := st.LoadSettings()
		if err != nil {
			return acme.Config{}, err
		}
		return acmeConfig(configDir, s), nil
	}, reloadFn(st, eng, opts), log)
	if err != nil {
		log.Error("acme manager could not start — serving with the self-signed certificate", "err", err)
	} else {
		log.Info("acme manager started; re-reads acme.* settings on every reconcile")
		go mgr.Run(ctx)
	}

	<-ctx.Done()
	log.Info("shutdown signal received, stopping GUI and engine")

	svc.StopAdvertise() // best-effort goodbye packets for anything self-advertised

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
		Enabled:         s.ACME.Enabled,
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

// defaultConfigDir is /config (the mounted volume root) when running inside a
// container, or an OS-conventional config directory otherwise — running the
// binary directly on the host (e.g. from an IDE) shouldn't default to writing
// at a filesystem root, which on Windows is exactly what the literal "/config"
// resolves to (C:\config). BRAMBLEGATE_CONFIG_DIR always overrides both.
func defaultConfigDir() string {
	if d := os.Getenv("BRAMBLEGATE_CONFIG_DIR"); d != "" {
		return d
	}
	if inContainer() {
		return "/config"
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "bramblegate")
	}
	return "./bramblegate-config" // last resort: os.UserConfigDir() needs $HOME/%AppData% set
}

// inContainer reports whether the process is running inside a container.
// /.dockerenv is injected by the container runtime itself (Docker, and most
// compatible runtimes), independent of the base image, so this works even
// against the distroless image this project ships (docs/repo-layout.md). The
// /proc/1/cgroup check catches non-Docker OCI runtimes (containerd, Kubernetes)
// that don't create /.dockerenv.
func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("docker")) ||
		bytes.Contains(data, []byte("containerd")) ||
		bytes.Contains(data, []byte("kubepods"))
}

// defaultGUIAddr is :8080 unless overridden by BRAMBLEGATE_GUI_ADDR.
func defaultGUIAddr() string {
	if a := os.Getenv("BRAMBLEGATE_GUI_ADDR"); a != "" {
		return a
	}
	return ":8080"
}

// defaultLogLevel is "info" unless overridden by BRAMBLEGATE_LOG_LEVEL.
func defaultLogLevel() string {
	if l := os.Getenv("BRAMBLEGATE_LOG_LEVEL"); l != "" {
		return l
	}
	return "info"
}

// defaultLogFormat is "json" (structured, machine-parseable — the sane default
// for a process whose stdout is normally read by `docker logs`/a log
// collector, not a human's terminal) unless overridden by
// BRAMBLEGATE_LOG_FORMAT; "text" gives slog's human-readable key=value form.
func defaultLogFormat() string {
	if f := os.Getenv("BRAMBLEGATE_LOG_FORMAT"); f != "" {
		return f
	}
	return "json"
}

// newLogger builds the process logger from the (flag- or env-sourced) level
// and format strings.
func newLogger(levelStr, format string) (*slog.Logger, error) {
	level, err := parseLogLevel(levelStr)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "", "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("invalid log format %q (want %q or %q)", format, "json", "text")
	}
	return slog.New(handler), nil
}

// parseLogLevel maps a flag/env string to a slog.Level.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want debug, info, warn, or error)", s)
	}
}
