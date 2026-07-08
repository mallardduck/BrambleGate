package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
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
	cfg := Config{ConfigDir: t.TempDir(), Domain: "dns.example.com", Provider: "cloudflare"}
	m := newManager(cfg, iss, func() error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	real, realk := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	writeCert(t, m, real, realk)
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

func TestReconcileIssuesWritesAndReloads(t *testing.T) {
	real, realk := makeCert(t, "dns.example.com", "Test Root CA", time.Now().AddDate(0, 3, 0))
	iss := &stubIssuer{cert: real, key: realk}
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
	if string(onDisk) != string(real) {
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

func TestNewManagerRejectsUnknownProvider(t *testing.T) {
	_, err := NewManager(Config{Provider: "nonesuch"}, func() error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	// exec/httpreq escape hatches are accepted.
	if _, err := NewManager(Config{Provider: "exec"}, func() error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("exec should be accepted: %v", err)
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

var errTest = errTestType("boom")

type errTestType string

func (e errTestType) Error() string { return string(e) }
