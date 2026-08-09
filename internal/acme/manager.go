package acme

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/lego"
)

const (
	defaultRenewBeforeDays = 30
	checkInterval          = 12 * time.Hour
	retryInterval          = 5 * time.Minute
)

// Manager keeps the DoT/DoH certificate valid in the background: it decides when
// a (re)issue is needed, obtains via the Issuer, writes the cert/key, and calls
// the reload callback so the running engine picks up the new certificate — the
// same store→configgen→engine.Reload path any config change uses
// (docs/certificates.md). Renewal is just this on a timer.
type Manager struct {
	cfg    Config
	issuer Issuer
	reload func() error
	log    *slog.Logger
	now    func() time.Time
}

// NewManager validates the provider and returns a Manager using the real lego
// issuer.
func NewManager(cfg Config, reload func() error, log *slog.Logger) (*Manager, error) {
	if _, ok := LookupProvider(cfg.Provider); !ok {
		return nil, fmt.Errorf("unsupported acme dns_provider %q (supported: %s; or use exec/httpreq)",
			cfg.Provider, strings.Join(SupportedProviders(), ", "))
	}
	enableDebugLogging(log)
	return newManager(cfg, &legoIssuer{cfg: cfg, log: log}, reload, log), nil
}

func newManager(cfg Config, issuer Issuer, reload func() error, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, issuer: issuer, reload: reload, log: log, now: time.Now}
}

func (m *Manager) certFile() string { return filepath.Join(m.cfg.ConfigDir, "certs", "cert.pem") }
func (m *Manager) keyFile() string  { return filepath.Join(m.cfg.ConfigDir, "certs", "key.pem") }

// caFile records which ACME directory URL issued the on-disk cert — the only
// way to tell a staging cert from a production one after the fact (the cert
// itself doesn't reliably self-identify this). Written alongside cert.pem/
// key.pem on every issuance, read back by needsIssue so flipping
// acme.production (or ca_directory_url) takes effect on the next reconcile
// instead of silently continuing to serve the old CA's cert until it happens
// to hit its renewal window.
func (m *Manager) caFile() string { return filepath.Join(m.cfg.ConfigDir, "certs", "issuer_ca.txt") }

// cacheDir holds a copy of the last cert/key issued per CA environment, so
// toggling acme.production back and forth doesn't force a fresh issuance (and
// burn Let's Encrypt's production rate limit) every time — see
// promoteFromCache/cacheCurrent.
func (m *Manager) cacheDir() string { return filepath.Join(m.cfg.ConfigDir, "certs", "cache") }

func (m *Manager) cacheCertFile(slug string) string {
	return filepath.Join(m.cacheDir(), slug+".cert.pem")
}

func (m *Manager) cacheKeyFile(slug string) string {
	return filepath.Join(m.cacheDir(), slug+".key.pem")
}

// caSlug turns a resolved ACME directory URL into a filesystem-safe cache key:
// "production"/"staging" for the two Let's Encrypt directories, or a short
// hash for anything else (a custom ca_directory_url, e.g. a local Pebble
// instance) so an arbitrary URL never has to be a literal filename.
func caSlug(caURL string) string {
	switch caURL {
	case lego.LEDirectoryProduction:
		return "production"
	case lego.LEDirectoryStaging:
		return "staging"
	default:
		sum := sha256.Sum256([]byte(caURL))
		return "custom-" + hex.EncodeToString(sum[:8])
	}
}

func (m *Manager) renewBefore() time.Duration {
	d := m.cfg.RenewBeforeDays
	if d <= 0 {
		d = defaultRenewBeforeDays
	}
	return time.Duration(d) * 24 * time.Hour
}

// Run reconciles once immediately, then on a timer until ctx is canceled. It
// never returns an error — issuance problems are logged and retried, so a DNS
// provider or connectivity hiccup can't take the whole process down (the engine
// keeps serving whatever cert is already on disk).
func (m *Manager) Run(ctx context.Context) {
	for {
		wait := m.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// reconcile issues/renews if needed and returns how long to wait before the next
// check (sooner after a failure so a transient error recovers quickly).
func (m *Manager) reconcile(ctx context.Context) time.Duration {
	need, reason := m.needsIssue()
	if !need {
		m.log.Debug("acme: certificate check, no action needed", "domain", m.cfg.Domain,
			"next_check_in", checkInterval)
		return checkInterval
	}

	if m.promoteFromCache() {
		if err := m.reload(); err != nil {
			m.log.Error("acme: promoted cached certificate but engine reload failed", "err", err)
			return retryInterval
		}
		m.log.Info("acme: reused cached certificate for this CA environment (no new issuance needed)",
			"domain", m.cfg.Domain, "ca", caDirURL(m.cfg))
		return checkInterval
	}

	m.log.Info("acme: obtaining certificate", "reason", reason, "domain", m.cfg.Domain,
		"provider", m.cfg.Provider, "ca", caDirURL(m.cfg))

	certPEM, keyPEM, err := m.issuer.Obtain(ctx)
	if err != nil {
		m.log.Error("acme: issuance failed, will retry", "err", err, "retry_in", retryInterval)
		return retryInterval
	}
	if err := m.writeCertKey(certPEM, keyPEM); err != nil {
		m.log.Error("acme: writing certificate failed, will retry", "err", err)
		return retryInterval
	}
	if err := m.cacheCurrent(certPEM, keyPEM); err != nil {
		// Non-fatal: the active cert is already written and correct, we've
		// just lost the fast-path for a future switch back to this environment.
		m.log.Warn("acme: could not cache issued certificate for future reuse", "err", err)
	}
	if err := m.reload(); err != nil {
		m.log.Error("acme: cert written but engine reload failed", "err", err)
		return retryInterval
	}
	m.log.Info("acme: certificate installed and applied", "domain", m.cfg.Domain,
		"production", m.cfg.Production && m.cfg.CADirectoryURL == "")
	return checkInterval
}

// promoteFromCache copies a previously-issued, still-valid certificate for the
// CURRENT CA environment out of the per-environment cache into the active
// cert.pem/key.pem, if one exists — so a Manager never calls Obtain when a
// perfectly good cert for this exact environment+domain is already sitting on
// disk from an earlier issuance. Returns false if there's nothing usable
// cached (never issued for this environment yet, wrong domain, or itself
// within the renewal window).
func (m *Manager) promoteFromCache() bool {
	slug := caSlug(caDirURL(m.cfg))
	certPEM, err := os.ReadFile(m.cacheCertFile(slug))
	if err != nil {
		return false
	}
	keyPEM, err := os.ReadFile(m.cacheKeyFile(slug))
	if err != nil {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if !coversDomain(cert, m.cfg.Domain) {
		return false
	}
	if remaining := cert.NotAfter.Sub(m.now()); remaining < m.renewBefore() {
		return false
	}
	if err := m.writeCertKey(certPEM, keyPEM); err != nil {
		m.log.Error("acme: promoting cached certificate failed", "err", err)
		return false
	}
	return true
}

// cacheCurrent saves a just-issued cert/key into the per-environment cache so
// a future switch back to this CA environment can reuse it via
// promoteFromCache instead of issuing again.
func (m *Manager) cacheCurrent(certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(m.cacheDir(), 0o755); err != nil {
		return err
	}
	slug := caSlug(caDirURL(m.cfg))
	if err := writeFileAtomic(m.cacheKeyFile(slug), keyPEM, 0o600); err != nil {
		return err
	}
	return writeFileAtomic(m.cacheCertFile(slug), certPEM, 0o644)
}

// needsIssue reports whether the on-disk cert must be (re)issued, with a reason.
// The self-signed bootstrap placeholder (issuer == subject) always triggers a
// real issuance; a real cert triggers only when it is for the wrong domain or is
// within the renewal window.
func (m *Manager) needsIssue() (bool, string) {
	raw, err := os.ReadFile(m.certFile())
	if err != nil {
		return true, "no certificate on disk"
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return true, "certificate is not valid PEM"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true, "certificate could not be parsed"
	}
	if bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return true, "self-signed placeholder in place"
	}
	if !coversDomain(cert, m.cfg.Domain) {
		return true, "certificate does not cover the configured domain"
	}
	if raw, err := os.ReadFile(m.caFile()); err != nil || string(raw) != caDirURL(m.cfg) {
		return true, "acme CA environment (production/ca_directory_url) changed since this certificate was issued"
	}
	if remaining := cert.NotAfter.Sub(m.now()); remaining < m.renewBefore() {
		return true, fmt.Sprintf("within renewal window (%s left)", remaining.Round(time.Hour))
	}
	return false, ""
}

func coversDomain(cert *x509.Certificate, domain string) bool {
	if strings.EqualFold(cert.Subject.CommonName, domain) {
		return true
	}
	for _, n := range cert.DNSNames {
		if strings.EqualFold(n, domain) {
			return true
		}
	}
	return false
}

// writeCertKey writes the key then the cert, each atomically (temp + rename in
// the same dir). We trigger the reload only after both are in place.
func (m *Manager) writeCertKey(certPEM, keyPEM []byte) error {
	m.log.Debug("acme: writing certificate and key", "cert_file", m.certFile(), "key_file", m.keyFile())
	if err := os.MkdirAll(filepath.Dir(m.certFile()), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(m.keyFile(), keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(m.certFile(), certPEM, 0o644); err != nil {
		return err
	}
	return writeFileAtomic(m.caFile(), []byte(caDirURL(m.cfg)), 0o644)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
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
	if err := tmp.Chmod(perm); err != nil {
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
		return err
	}
	return nil
}
