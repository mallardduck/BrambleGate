package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// Config is everything the issuer/manager needs. Credentials are NOT here — the
// provider reads those from the environment. Built from model.ACME by the caller
// so this package stays decoupled from model.
type Config struct {
	ConfigDir       string // /config root
	Domain          string
	Email           string
	Provider        string // acme.dns_provider
	Production      bool
	CADirectoryURL  string // overrides Production when set (e.g. Pebble)
	RenewBeforeDays int    // 0 → default (30)
}

// Issuer obtains a certificate chain + private key (PEM) for the configured
// domain. Abstracted so the Manager's renew/reload logic is testable without a
// real ACME server.
type Issuer interface {
	Obtain(ctx context.Context) (certPEM, keyPEM []byte, err error)
}

// caDirURL resolves the ACME directory: an explicit override wins, otherwise
// production or staging. Staging is the default so nothing burns prod rate limits
// until the user opts in (docs/certificates.md).
func caDirURL(c Config) string {
	if c.CADirectoryURL != "" {
		return c.CADirectoryURL
	}
	if c.Production {
		return lego.LEDirectoryProduction
	}
	return lego.LEDirectoryStaging
}

// legoIssuer is the real ACME DNS-01 issuer.
type legoIssuer struct {
	cfg Config
	// newProvider builds the DNS-01 solver; defaults to the curated registry.
	// Overridden only in integration tests to solve against a test challenge
	// server (Pebble challtestsrv).
	newProvider func(name string) (challenge.Provider, error)
	// challengeOpts tunes the DNS-01 solver. Empty in production (full propagation
	// checks against public DNS, which is correct for real domains). Integration
	// tests set it to point the propagation check at the test DNS server, since a
	// throwaway TLD like `brambletest` isn't in public DNS.
	challengeOpts []dns01.ChallengeOption
}

func (i *legoIssuer) providerFactory() func(string) (challenge.Provider, error) {
	if i.newProvider != nil {
		return i.newProvider
	}
	return newChallengeProvider
}

// Obtain registers the account (idempotent) and issues the certificate via the
// configured DNS-01 provider.
func (i *legoIssuer) Obtain(ctx context.Context) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	key, err := loadOrCreateAccountKey(accountDir(i.cfg.ConfigDir))
	if err != nil {
		return nil, nil, err
	}
	user := &acmeUser{email: i.cfg.Email, key: key}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = caDirURL(i.cfg)
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("acme client: %w", err)
	}

	provider, err := i.providerFactory()(i.cfg.Provider)
	if err != nil {
		return nil, nil, err
	}
	if err := client.Challenge.SetDNS01Provider(provider, i.challengeOpts...); err != nil {
		return nil, nil, fmt.Errorf("configure dns-01 provider %q: %w", i.cfg.Provider, err)
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, nil, fmt.Errorf("acme account registration: %w", err)
	}
	user.reg = reg

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{i.cfg.Domain},
		Bundle:  true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("obtain certificate for %q: %w", i.cfg.Domain, err)
	}
	return res.Certificate, res.PrivateKey, nil
}

// acmeUser implements registration.User backed by a persisted account key.
type acmeUser struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

func accountDir(configDir string) string {
	return filepath.Join(configDir, "certs", "account")
}

// loadOrCreateAccountKey loads the persistent ACME account key, creating it on
// first use. The account key is long-lived and tied to the registration.
func loadOrCreateAccountKey(dir string) (crypto.PrivateKey, error) {
	path := filepath.Join(dir, "account.key")
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("account key %s is not valid PEM", path)
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create account dir: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write account key: %w", err)
	}
	return key, nil
}
