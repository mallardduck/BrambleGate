package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Settings is the Phase 1 subset of docs/config-schema.md that the forward-only
// build needs: where to forward, which listeners to open, and the ACME hostname
// (used here only as the self-signed cert's name until Phase 4 issues a real
// one). It lives in internal/cli as a deliberate stand-in — Phase 2 moves the
// full schema into the model module and renders via configgen.
type Settings struct {
	UpstreamDNS UpstreamDNS `yaml:"upstream_dns"`
	Listeners   Listeners   `yaml:"listeners"`
	ACME        ACME        `yaml:"acme"`
}

type UpstreamDNS struct {
	Address  string `yaml:"address"`  // host:port of the user's ad-block resolver
	Protocol string `yaml:"protocol"` // plain | dot (doh/doq deferred)
}

type Listeners struct {
	Plain Listener `yaml:"plain"`
	DoT   Listener `yaml:"dot"`
}

type Listener struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type ACME struct {
	Domain string `yaml:"domain"`
}

// LoadSettings reads and validates <configDir>/custom/settings.yaml.
func LoadSettings(configDir string) (Settings, error) {
	var s Settings
	path := filepath.Join(configDir, "custom", "settings.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("read settings: %w", err)
	}
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return s, fmt.Errorf("invalid settings (%s): %w", path, err)
	}
	return s, nil
}

func (s Settings) validate() error {
	if !s.Listeners.Plain.Enabled && !s.Listeners.DoT.Enabled {
		return fmt.Errorf("no listeners enabled: enable at least one of listeners.plain / listeners.dot")
	}
	if s.UpstreamDNS.Address == "" {
		return fmt.Errorf("upstream_dns.address is required")
	}
	host, port, err := net.SplitHostPort(s.UpstreamDNS.Address)
	if err != nil {
		return fmt.Errorf("upstream_dns.address %q must be host:port: %w", s.UpstreamDNS.Address, err)
	}
	if host == "" {
		return fmt.Errorf("upstream_dns.address %q is missing a host", s.UpstreamDNS.Address)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("upstream_dns.address %q has a non-numeric port", s.UpstreamDNS.Address)
	}
	switch s.UpstreamDNS.Protocol {
	case "", "plain", "dot":
	default:
		return fmt.Errorf("upstream_dns.protocol %q must be plain or dot", s.UpstreamDNS.Protocol)
	}
	if s.Listeners.Plain.Enabled && s.Listeners.Plain.Port == 0 {
		return fmt.Errorf("listeners.plain.port is required when plain is enabled")
	}
	if s.Listeners.DoT.Enabled {
		if s.Listeners.DoT.Port == 0 {
			return fmt.Errorf("listeners.dot.port is required when dot is enabled")
		}
		if s.ACME.Domain == "" {
			return fmt.Errorf("acme.domain is required when the DoT listener is enabled (used as the certificate name)")
		}
	}
	return nil
}
