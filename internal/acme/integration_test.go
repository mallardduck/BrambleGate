//go:build acme_integration

// This test proves a real ACME DNS-01 issuance end-to-end against Pebble (Let's
// Encrypt's test CA) with no public exposure and no real domain — the same thing
// a homelab does, just with a throwaway CA. Run it via deploy/test/run-acme-integration.sh,
// which stands up deploy/test/pebble-compose.yml and sets:
//
//	PEBBLE_DIR         e.g. https://localhost:14000/dir
//	PEBBLE_CA          path to pebble's minica.pem (so lego trusts the ACME TLS)
//	CHALLTESTSRV_URL   e.g. http://localhost:8055
package acme

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
)

// challtestsrvProvider solves DNS-01 by writing the TXT record into Pebble's
// challenge test server via its management API.
type challtestsrvProvider struct{ base string }

func (p *challtestsrvProvider) Present(domain, _, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.post("/set-txt", map[string]string{"host": info.FQDN, "value": info.Value})
}

func (p *challtestsrvProvider) CleanUp(domain, _, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.post("/clear-txt", map[string]string{"host": info.FQDN})
}

func (p *challtestsrvProvider) post(path string, body map[string]string) error {
	raw, _ := json.Marshal(body)
	resp, err := http.Post(p.base+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestPebbleDNS01Issuance(t *testing.T) {
	dir := os.Getenv("PEBBLE_DIR")
	ca := os.Getenv("PEBBLE_CA")
	chall := os.Getenv("CHALLTESTSRV_URL")
	if dir == "" || ca == "" || chall == "" {
		t.Skip("set PEBBLE_DIR, PEBBLE_CA, CHALLTESTSRV_URL (see deploy/test/run-acme-integration.sh)")
	}
	// Make lego trust Pebble's self-signed ACME TLS cert.
	t.Setenv("LEGO_CA_CERTIFICATES", ca)

	iss := &legoIssuer{
		cfg: Config{
			ConfigDir:      t.TempDir(),
			Domain:         "dns.brambletest",
			Email:          "test@brambletest",
			CADirectoryURL: dir,
		},
		newProvider: func(string) (challenge.Provider, error) {
			return &challtestsrvProvider{base: chall}, nil
		},
		// The throwaway TLD isn't in public DNS, so point lego's propagation
		// pre-check at challtestsrv's DNS (which has the TXT) and don't require
		// the authoritative-NS lookup.
		challengeOpts: []dns01.ChallengeOption{
			dns01.AddRecursiveNameservers([]string{"127.0.0.1:8053"}),
			dns01.DisableAuthoritativeNssPropagationRequirement(),
		},
	}

	certPEM, keyPEM, err := iss.Obtain(context.Background())
	if err != nil {
		t.Fatalf("Obtain against Pebble: %v", err)
	}
	if len(keyPEM) == 0 {
		t.Fatal("no private key returned")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("cert is not PEM:\n%s", certPEM)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}
	// A real CA-issued cert: issuer != subject, and it covers our domain.
	if bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		t.Fatal("expected a CA-issued cert, got a self-signed one")
	}
	if !coversDomain(cert, "dns.brambletest") {
		t.Fatalf("issued cert does not cover the domain: %v", cert.DNSNames)
	}
	if !strings.Contains(cert.Issuer.CommonName, "Pebble") {
		t.Logf("issuer CN = %q (expected a Pebble intermediate)", cert.Issuer.CommonName)
	}
}
