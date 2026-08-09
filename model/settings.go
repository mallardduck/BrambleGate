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

// VLAN mirrors one of the user's real network VLANs (BrambleGate is not the
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
// the fallback when ACME is disabled/unconfigured.
//
// Provider credentials are NOT stored here — they are read from environment
// variables the way lego expects (e.g. CLOUDFLARE_DNS_API_TOKEN), keeping secrets
// out of the config volume. See docs/certificates.md for the per-provider vars.
type ACME struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	Domain      string `yaml:"domain" json:"domain"`
	Email       string `yaml:"email" json:"email"`
	DNSProvider string `yaml:"dns_provider" json:"dns_provider"`
	// Production selects the Let's Encrypt production CA. Default (false) uses the
	// LE staging CA — certificates that are NOT publicly trusted, so nothing can
	// accidentally burn production rate limits until the user opts in.
	Production bool `yaml:"production" json:"production"`
	// CADirectoryURL overrides the ACME server entirely (custom CA or a test
	// server like Pebble). When set it takes precedence over Production.
	CADirectoryURL  string `yaml:"ca_directory_url,omitempty" json:"ca_directory_url,omitempty"`
	RenewBeforeDays int    `yaml:"renew_before_days" json:"renew_before_days"`
}

// MDNS configures the mdnsbridge plugin (docs/plugins.md). Publishing is driven
// by selectors, not a coarse mode: a discovery is served live when it matches any
// AutoPublish selector; otherwise it is a candidate to approve/promote in the GUI.
type MDNS struct {
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	Interfaces []string `yaml:"interfaces" json:"interfaces"` // [] or ["all"] = all
	// ServiceTypes to browse (e.g. "_http._tcp"); empty uses the plugin
	// defaults (a curated common-types list). ["all"] discovers service
	// types dynamically via the DNS-SD meta-query instead of a fixed list.
	ServiceTypes []string `yaml:"service_types,omitempty" json:"service_types,omitempty"`
	// Suffix is the default zone discovered names map into; empty → home.arpa.
	Suffix string `yaml:"suffix,omitempty" json:"suffix,omitempty"`
	// AutoPublish serves a discovery live when any selector matches (OR). Empty
	// means nothing is auto-published (approve/promote manually).
	AutoPublish []Selector `yaml:"auto_publish,omitempty" json:"auto_publish,omitempty"`
	// Naming overrides the suffix for matching discoveries.
	Naming []NamingRule `yaml:"naming,omitempty" json:"naming,omitempty"`
	// Advertise controls self-advertising this server's own DNS service(s) via
	// mDNS-SD. Independent of Enabled above (which governs discovering OTHER
	// devices) — broadcasting "there is a DNS resolver here" to the whole L2
	// segment is a bigger exposure than passively browsing other devices'
	// announcements, so it is off by default even when discovery is on.
	Advertise MDNSAdvertise `yaml:"advertise" json:"advertise"`
}

// MDNSAdvertise configures self-advertisement of this server's own DNS
// service(s) (plain DNS, and DoT/DoH/DoQ when those listeners are enabled) via
// mDNS-SD (draft-liu-add-dnssd-edns-01 for the encrypted transports; the
// IANA-registered "domain" service name for plain port 53).
type MDNSAdvertise struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// DefaultSettings returns a minimal, immediately-usable configuration seeded on
// first run when no settings.yaml exists yet (see internal/cli). It brings the
// container up as a working plain-DNS front door that forwards to a public
// resolver, so a fresh user gets a resolving server out of the box and can then
// point upstream_dns at their own ad-block resolver (PiHole/AdGuard/Technitium)
// and add VLANs/records. Encrypted listeners, ACME, and mDNS stay off until
// deliberately enabled — they need user-supplied domains/credentials.
func DefaultSettings() Settings {
	return Settings{
		UpstreamDNS: UpstreamTarget{Address: "1.1.1.1:53", Protocol: "plain"},
		Listeners: Listeners{
			Plain: Listener{Enabled: true, Port: 53},
		},
		ACME: ACME{RenewBeforeDays: 30},
	}
}

// EncryptedListenerEnabled reports whether any transport that needs a
// certificate (DoT/DoH/DoQ) is turned on — used by configgen to require ACME
// settings / a cert.
func (s Settings) EncryptedListenerEnabled() bool {
	return s.Listeners.DoT.Enabled || s.Listeners.DoH.Enabled || s.Listeners.DoQ.Enabled
}
