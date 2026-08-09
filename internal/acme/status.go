package acme

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v4/lego"
)

// Status is a read-only snapshot of the on-disk cert for display (e.g. the GUI
// dashboard). It re-derives straight from certs/cert.pem + certs/issuer_ca.txt
// rather than tracking a Manager's in-memory state, so it works whether or not
// a Manager is even running (ACME disabled, self-signed only, etc.) — the same
// files needsIssue reads are the single source of truth either way.
type Status struct {
	Present    bool // a cert.pem exists and parsed successfully
	SelfSigned bool // bootstrap placeholder (issuer == subject) — see ensureSelfSignedCert
	Domain     string
	DNSNames   []string
	NotAfter   time.Time
	// Environment is "production", "staging", "custom" (a ca_directory_url
	// override), or "" when unknown (no issuer_ca.txt — e.g. the self-signed
	// placeholder, which predates any ACME issuance).
	Environment string
}

// DaysRemaining returns the whole days until NotAfter (negative if expired).
// Only meaningful when Present is true.
func (s Status) DaysRemaining(now time.Time) int {
	return int(s.NotAfter.Sub(now).Hours() / 24)
}

// ReadStatus inspects <configDir>/certs for the current cert. Missing or
// unparsable files are not errors here — an absent cert is a normal state
// (fresh install, ACME never enabled) and this is a best-effort status
// display, not a decision point the way Manager.needsIssue is.
func ReadStatus(configDir string) Status {
	raw, err := os.ReadFile(filepath.Join(configDir, "certs", "cert.pem"))
	if err != nil {
		return Status{}
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return Status{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Status{}
	}

	st := Status{
		Present:    true,
		SelfSigned: bytes.Equal(cert.RawIssuer, cert.RawSubject),
		Domain:     cert.Subject.CommonName,
		DNSNames:   cert.DNSNames,
		NotAfter:   cert.NotAfter,
	}
	if caRaw, err := os.ReadFile(filepath.Join(configDir, "certs", "issuer_ca.txt")); err == nil {
		switch string(caRaw) {
		case lego.LEDirectoryProduction:
			st.Environment = "production"
		case lego.LEDirectoryStaging:
			st.Environment = "staging"
		default:
			st.Environment = "custom"
		}
	}
	return st
}
