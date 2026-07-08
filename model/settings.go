package model

// Settings mirrors /config/settings.yaml (docs/config-schema.md). It is the
// shared vocabulary between the GUI (writes it via store), configgen (reads it
// to render the Corefile), and the runtime. Pure data — no behavior, no deps.
type Settings struct {
	VLANs       []VLAN         `yaml:"vlans" json:"vlans"`
	UpstreamDNS UpstreamTarget `yaml:"upstream_dns" json:"upstream_dns"`
	Listeners   Listeners      `yaml:"listeners" json:"listeners"`
	ACME        ACME           `yaml:"acme" json:"acme"`
	MDNS        MDNS           `yaml:"mdns" json:"mdns"`
}

// VLAN mirrors one of the user's real network VLANs (BrambleDNS is not the
// authority for VLANs — the network gear is; this just lets the user declare the
// same name→subnet mapping). Used for split-horizon matching by client source
// address and for mDNS interface scoping. A VLAN may span more than one CIDR
// (e.g. an IPv4 and an IPv6 prefix, or several subnets).
type VLAN struct {
	Name  string   `yaml:"name" json:"name"`
	CIDRs []string `yaml:"cidrs" json:"cidrs"`
}

// UpstreamTarget is where anything not owned by localrecords/mdnsbridge is
// forwarded — the user's existing ad-block resolver. Just a config value.
type UpstreamTarget struct {
	Address  string `yaml:"address" json:"address"`   // host:port
	Protocol string `yaml:"protocol" json:"protocol"` // plain | dot | doh
}

// Listeners is the set of DNS transports this server terminates.
type Listeners struct {
	Plain Listener `yaml:"plain" json:"plain"`
	DoT   Listener `yaml:"dot" json:"dot"`
	DoH   Listener `yaml:"doh" json:"doh"`
	DoQ   Listener `yaml:"doq" json:"doq"`
}

type Listener struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	Port    int  `yaml:"port" json:"port"`
}

// ACME configures DNS-01 certificate issuance for the encrypted listeners
// (docs/certificates.md). Domain is also used as the self-signed cert name in
// the pre-Phase-4 fallback.
type ACME struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	Domain          string `yaml:"domain" json:"domain"`
	Email           string `yaml:"email" json:"email"`
	DNSProvider     string `yaml:"dns_provider" json:"dns_provider"`
	RenewBeforeDays int    `yaml:"renew_before_days" json:"renew_before_days"`
}

// MDNS configures the mdnsbridge plugin (docs/plugins.md).
type MDNS struct {
	Enabled            bool     `yaml:"enabled" json:"enabled"`
	DefaultPublishMode string   `yaml:"default_publish_mode" json:"default_publish_mode"` // require-approval | auto-publish
	Interfaces         []string `yaml:"interfaces" json:"interfaces"`
}

// EncryptedListenerEnabled reports whether any transport that needs a
// certificate (DoT/DoH/DoQ) is turned on — used by configgen to require ACME
// settings / a cert.
func (s Settings) EncryptedListenerEnabled() bool {
	return s.Listeners.DoT.Enabled || s.Listeners.DoH.Enabled || s.Listeners.DoQ.Enabled
}
