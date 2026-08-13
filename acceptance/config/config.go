// Package config loads the acceptance suite's target description: which
// running BrambleGate instance to check, and what each check should expect.
// Plain YAML, matching the rest of the project's no-heavy-config-format stance
// (dev-docs/repo-layout.md's "Docker build notes").
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the acceptance.yaml shape. See acceptance.example.yaml.
type Config struct {
	Target   Target       `yaml:"target"`
	Domain   string       `yaml:"domain"`
	VLANs    []VLAN       `yaml:"vlans"`
	Upstream Upstream     `yaml:"upstream"`
	MDNS     MDNS         `yaml:"mdns"`
	Hosts    []HostsCheck `yaml:"hosts_overrides"`
	Mobile   Mobile       `yaml:"mobile"`
}

// Target is the BrambleGate instance under test.
type Target struct {
	// APIBase is the dashboard/API base URL, e.g. https://192.168.1.164:8443.
	APIBase string `yaml:"api_base"`
	// DNSAddr is the DNS listener address (plain/DoT/DoH share a host), e.g. 192.168.1.164.
	DNSAddr string `yaml:"dns_addr"`
}

// VLAN is one VLAN's split-horizon expectation for Domain (roadmap.md Scenario 2).
// Local-tier only: meaningful only when the suite runs from a device on this VLAN.
type VLAN struct {
	Name           string `yaml:"name"`
	ExpectValue    string `yaml:"expect_value,omitempty"`
	ExpectNXDOMAIN bool   `yaml:"expect_nxdomain,omitempty"`
}

// Upstream is the real ad-block resolver BrambleGate forwards to (Scenario 4).
type Upstream struct {
	Address string `yaml:"address"`
	// TestDomain is an external (not BrambleGate-owned) name to compare
	// answers for between BrambleGate and Upstream directly — proves
	// transparent forwarding, not any BrambleGate-specific behavior.
	TestDomain string `yaml:"test_domain,omitempty"`
}

// MDNS names a previously-promoted record to confirm it still tracks live
// mDNS state (Scenario 3, network-observable half only).
type MDNS struct {
	PromotedName string `yaml:"promoted_name"`
}

// HostsCheck is one hosts.yaml override expectation (Phase 8).
type HostsCheck struct {
	Name     string `yaml:"name"`
	ExpectIP string `yaml:"expect_ip"`
}

// Mobile gates the ADB-based tier (deferred; see mobile/adb.go). Off by default.
type Mobile struct {
	Enabled bool `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
