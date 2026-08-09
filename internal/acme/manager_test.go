package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"testing"
	"time"
)

// stubIssuer returns a canned CA-signed cert (issuer != subject) or an error.
type stubIssuer struct {
	calls   int
	cert    []byte
	key     []byte
	failErr error
}

func (s *stubIssuer) Obtain(context.Context) ([]byte, []byte, error) {
	s.calls++
	if s.failErr != nil {
		return nil, nil, s.failErr
	}
	return s.cert, s.key, nil
}

func testManager(t *testing.T, iss Issuer) *Manager {
	t.Helper()
	cfg := Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "cloudflare", Enabled: true}
	m := newManager(cfg, iss, func() error { return nil }, slog.New(slog.DiscardHandler))
	return m
}

// makeCert builds a PEM cert for domain with the given issuer CN and validity.
func makeCert(t *testing.T, domain, issuerCN string, notAfter time.Time) ([]byte, []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	selfSigned := issuerCN == domain
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	parent := tmpl
	if !selfSigned {
		parent = &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: issuerCN}}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func writeCert(t *testing.T, m *Manager, certPEM, keyPEM []byte) {
	t.Helper()
	if err := m.writeCertKey(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsIssue(t *testing.T) {
	m := testManager(t, &stubIssuer{})

	// No cert on disk.
	if need, _ := m.needsIssue(); !need {
		t.Fatal("missing cert should need issuance")
	}

	// Self-signed placeholder (issuer == subject).
	ss, ssk := makeCert(t, "dns.example.com", "dns.example.com", time.Now().AddDate(1, 0, 0))
	writeCert(t, m, ss, ssk)
	if need, reason := m.needsIssue(); !need {
		t.Fatalf("self-signed placeholder should need issuance, got no: %s", reason)
	}

	// Real CA cert, far from expiry → no issuance.
	realCert, realKey := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	writeCert(t, m, realCert, realKey)
	if need, reason := m.needsIssue(); need {
		t.Fatalf("valid real cert should not need issuance, got: %s", reason)
	}

	// Real cert within the renewal window (default 30d) → renew.
	soon, soonk := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 0, 10))
	writeCert(t, m, soon, soonk)
	if need, _ := m.needsIssue(); !need {
		t.Fatal("cert within renewal window should need renewal")
	}

	// Real cert for a different domain → reissue.
	other, otherk := makeCert(t, "other.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	writeCert(t, m, other, otherk)
	if need, _ := m.needsIssue(); !need {
		t.Fatal("cert for the wrong domain should need reissue")
	}
}

func TestNeedsIssueOnCAEnvironmentChange(t *testing.T) {
	m := testManager(t, &stubIssuer{})
	realCert, realKey := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	writeCert(t, m, realCert, realKey)
	if need, reason := m.needsIssue(); need {
		t.Fatalf("freshly-issued cert should not need issuance yet, got: %s", reason)
	}

	// Flip staging -> production with no new cert on disk: the old (untrusted
	// staging) cert must not keep being served silently.
	m.cfg.Production = true
	if need, reason := m.needsIssue(); !need {
		t.Fatal("switching to production should require reissuance")
	} else if reason == "" {
		t.Fatal("expected a reason")
	}

	// Reissuing (writeCertKey records the new CA) clears the mismatch.
	writeCert(t, m, realCert, realKey)
	if need, reason := m.needsIssue(); need {
		t.Fatalf("reissuing under the new CA should satisfy the check, got: %s", reason)
	}
}

func TestReconcileTogglingCAEnvironmentReusesCache(t *testing.T) {
	stagingCert, stagingKey := makeCert(t, "dns.example.com", "Staging Root CA", time.Now().AddDate(0, 3, 0))
	prodCert, prodKey := makeCert(t, "dns.example.com", "Production Root CA", time.Now().AddDate(0, 3, 0))
	iss := &stubIssuer{}
	m := testManager(t, iss)
	m.reload = func() error { return nil }

	// Issue for staging (the default): one real Obtain call.
	iss.cert, iss.key = stagingCert, stagingKey
	m.reconcile(context.Background())
	if iss.calls != 1 {
		t.Fatalf("expected 1 issuance for staging, got %d", iss.calls)
	}
	onDisk, _ := os.ReadFile(m.certFile())
	if string(onDisk) != string(stagingCert) {
		t.Fatal("staging cert was not written to disk")
	}

	// Switch to production: no cache yet for production, so a real issuance happens.
	m.cfg.Production = true
	iss.cert, iss.key = prodCert, prodKey
	m.reconcile(context.Background())
	if iss.calls != 2 {
		t.Fatalf("expected a 2nd issuance for production, got %d", iss.calls)
	}
	onDisk, _ = os.ReadFile(m.certFile())
	if string(onDisk) != string(prodCert) {
		t.Fatal("production cert was not written to disk")
	}

	// Switch back to staging: must reuse the cached staging cert, not call Obtain again.
	m.cfg.Production = false
	m.reconcile(context.Background())
	if iss.calls != 2 {
		t.Fatalf("switching back to staging should reuse the cache, not reissue; got %d calls", iss.calls)
	}
	onDisk, _ = os.ReadFile(m.certFile())
	if string(onDisk) != string(stagingCert) {
		t.Fatal("expected the cached staging cert to be promoted back to the active cert")
	}

	// And back to production once more: also cached now, still no 3rd issuance.
	m.cfg.Production = true
	m.reconcile(context.Background())
	if iss.calls != 2 {
		t.Fatalf("switching back to production should reuse the cache too; got %d calls", iss.calls)
	}
	onDisk, _ = os.ReadFile(m.certFile())
	if string(onDisk) != string(prodCert) {
		t.Fatal("expected the cached production cert to be promoted back to the active cert")
	}
}

func TestReconcileIssuesWritesAndReloads(t *testing.T) {
	realCert, realKey := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	iss := &stubIssuer{cert: realCert, key: realKey}
	reloaded := 0
	m := testManager(t, iss)
	m.reload = func() error { reloaded++; return nil }

	// Start from a self-signed placeholder so reconcile does work.
	ss, ssk := makeCert(t, "dns.example.com", "dns.example.com", time.Now().AddDate(1, 0, 0))
	writeCert(t, m, ss, ssk)

	wait := m.reconcile(context.Background())
	if iss.calls != 1 || reloaded != 1 {
		t.Fatalf("expected one obtain+reload, got calls=%d reloaded=%d", iss.calls, reloaded)
	}
	if wait != checkInterval {
		t.Fatalf("successful reconcile should wait checkInterval, got %s", wait)
	}
	onDisk, _ := os.ReadFile(m.certFile())
	if string(onDisk) != string(realCert) {
		t.Fatal("issued cert was not written to disk")
	}

	// Now the real cert is in place → no further work, no extra reload.
	if wait := m.reconcile(context.Background()); wait != checkInterval || iss.calls != 1 || reloaded != 1 {
		t.Fatalf("idempotent reconcile should be a no-op, got calls=%d reloaded=%d wait=%s", iss.calls, reloaded, wait)
	}
}

func TestReconcileFailureRetriesSoonAndKeepsServing(t *testing.T) {
	iss := &stubIssuer{failErr: errTest}
	m := testManager(t, iss)
	// Self-signed placeholder stays in place after a failed issuance.
	ss, ssk := makeCert(t, "dns.example.com", "dns.example.com", time.Now().AddDate(1, 0, 0))
	writeCert(t, m, ss, ssk)

	wait := m.reconcile(context.Background())
	if wait != retryInterval {
		t.Fatalf("failed issuance should retry soon, got %s", wait)
	}
	if need, _ := m.needsIssue(); !need {
		t.Fatal("placeholder must remain (still needs issuance) after a failure")
	}
}

// Provider validity is checked on every reconcile (against the freshly
// reloaded config), not just once at construction — a bad acme.dns_provider
// set via the GUI mid-session must be caught too, not just one set at boot.
func TestReconcileRejectsUnknownProvider(t *testing.T) {
	iss := &stubIssuer{}
	m := newManager(Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "nonesuch", Enabled: true},
		iss, func() error { return nil }, slog.New(slog.DiscardHandler))
	if wait := m.reconcile(context.Background()); wait != retryInterval {
		t.Fatalf("unsupported provider should retry, got wait=%s", wait)
	}
	if iss.calls != 0 {
		t.Fatal("must not attempt issuance with an unsupported provider")
	}
}

// exec/httpreq are provider escape hatches and must be accepted.
func TestReconcileAcceptsEscapeHatchProviders(t *testing.T) {
	iss := &stubIssuer{}
	m := newManager(Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "exec", Enabled: true},
		iss, func() error { return nil }, slog.New(slog.DiscardHandler))
	m.reconcile(context.Background())
	if iss.calls != 1 {
		t.Fatalf("exec provider should be accepted and proceed to issuance, got %d calls", iss.calls)
	}
}

// A Manager is now started unconditionally regardless of acme.enabled at boot
// (see internal/cli) — reconcile itself must no-op when the freshly reloaded
// config says ACME is disabled, rather than needing to never be constructed.
func TestReconcileNoOpWhenDisabled(t *testing.T) {
	iss := &stubIssuer{}
	m := newManager(Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "cloudflare", Enabled: false},
		iss, func() error { return nil }, slog.New(slog.DiscardHandler))
	if wait := m.reconcile(context.Background()); wait != checkInterval {
		t.Fatalf("disabled should just wait the normal interval, got %s", wait)
	}
	if iss.calls != 0 {
		t.Fatal("must not attempt issuance while disabled")
	}
}

// The whole point: a config change (e.g. flipping acme.enabled or
// acme.production via the GUI) must take effect on the Manager's very next
// reconcile, without recreating it — that's what loadCfg is for.
func TestReconcilePicksUpLiveConfigChanges(t *testing.T) {
	iss := &stubIssuer{}
	cfg := Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "cloudflare", Enabled: false}
	realCert, realKey := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	var loads int
	m := newManager(cfg, iss, func() error { return nil }, slog.New(slog.DiscardHandler))
	m.loadCfg = func() (Config, error) { loads++; return cfg, nil }

	if wait := m.reconcile(context.Background()); wait != checkInterval || iss.calls != 0 {
		t.Fatalf("should be a no-op while disabled, got wait=%s calls=%d", wait, iss.calls)
	}

	// Simulate the GUI flipping acme.enabled on and saving — same *Manager*,
	// no restart, no reconstruction.
	cfg.Enabled = true
	iss.cert, iss.key = realCert, realKey

	m.reconcile(context.Background())
	if loads < 2 {
		t.Fatal("reconcile must call loadCfg again on the next tick")
	}
	if iss.calls != 1 {
		t.Fatalf("enabling ACME should trigger issuance on the very next reconcile, got %d calls", iss.calls)
	}
}

func TestCADirURL(t *testing.T) {
	if got := caDirURL(Config{}); got == "" || got == "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatalf("default should be staging, got %q", got)
	}
	if got := caDirURL(Config{Production: true}); got != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatalf("production should be LE prod, got %q", got)
	}
	if got := caDirURL(Config{Production: true, CADirectoryURL: "https://pebble:14000/dir"}); got != "https://pebble:14000/dir" {
		t.Fatalf("override should win, got %q", got)
	}
}

var errTest = testError("boom")

type testError string

func (e testError) Error() string { return string(e) }
