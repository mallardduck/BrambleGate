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
	Cache       CacheTuning    `yaml:"cache" json:"cache"`
	Log         LogTuning      `yaml:"log" json:"log"`
	Errors      ErrorsTuning   `yaml:"errors" json:"errors"`
	// BufsizeDisabled turns off the bufsize hardening measure (on by default,
	// rendered as `bufsize 1232` — the plugin's own documented safe default —
	// in every server block). Caps EDNS0 UDP payload size to prevent IP
	// fragmentation, an RFC 6891 concern and a mitigation for certain
	// reflection/amplification vectors. Not a tunable size — 1232 is the value
	// the plugin's own docs recommend and there's no BrambleGate use case that
	// needs a different one; this is a disable-only knob for the rare case it
	// causes trouble with a particular client/upstream.
	BufsizeDisabled bool `yaml:"bufsize_disabled,omitempty" json:"bufsize_disabled,omitempty"`
}

// CacheTuning controls the cache plugin's resilience/efficiency knobs.
// Both default ON with a fixed, sane value —
// unlike CoreDNS's own stock defaults (serve_stale off, prefetch off) — since a
// homelab appliance benefits out of the box from riding out a brief upstream
// blip and from not re-fetching popular names right before they expire. Each is
// a plain "disabled" bool rather than a tunable duration/amount: the rendered
// values are the plugins' own documented recommended defaults, and there's no
// BrambleGate use case that needs to move them — only to turn one off if it
// ever causes surprising behavior for a given upstream.
//
// Note: cache is entirely omitted (regardless of these settings) whenever
// upstream_dns.ecs_enabled is on — see configgen.writeCache.
type CacheTuning struct {
	// ServeStaleDisabled turns off `serve_stale` (rendered as `serve_stale 1h
	// immediate` when enabled — CoreDNS's own default duration/refresh mode,
	// just switched on rather than left off).
	ServeStaleDisabled bool `yaml:"serve_stale_disabled,omitempty" json:"serve_stale_disabled,omitempty"`
	// PrefetchDisabled turns off `prefetch` (rendered as `prefetch 10 1m 10%`
	// when enabled — the plugin's own documented defaults).
	PrefetchDisabled bool `yaml:"prefetch_disabled,omitempty" json:"prefetch_disabled,omitempty"`
}

// LogTuning controls the log plugin. Zero
// value matches CoreDNS's own default and BrambleGate's prior behavior: log
// everything, unconditionally — so existing installs aren't silently changed.
// Unlike CacheTuning, there is a real "turn it off" case here (CoreDNS's own
// docs note the plugin costs performance on busy servers), hence a distinct
// Disabled bool rather than reusing a *Disabled naming convention tied to one
// specific sub-feature.
type LogTuning struct {
	// Disabled omits the log directive entirely.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// Classes restricts logged responses to specific classes (success, denial,
	// error, all — CoreDNS's own class names). Empty means every response is
	// logged, the same as omitting `class` in the Corefile.
	Classes []string `yaml:"classes,omitempty" json:"classes,omitempty"`
}

// ErrorsTuning controls the errors plugin's consolidate option (see
// CacheTuning's doc comment for the general "disable-only, fixed sane
// default" shape this follows). On by default: repeated identical failures
// (a flaky upstream timing out) collapse into a periodic summary instead of
// one line per error. Not further tunable — there's no BrambleGate use case
// that needs a different window/pattern than "consolidate upstream timeouts".
type ErrorsTuning struct {
	// ConsolidateDisabled turns off consolidate, reverting to one log line
	// per error.
	ConsolidateDisabled bool `yaml:"consolidate_disabled,omitempty" json:"consolidate_disabled,omitempty"`
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
	// ECS enables EDNS0 Client Subnet: the querying client's real source IP is
	// attached to the forwarded query so Address can apply per-client policy
	// (e.g. PiHole/AdGuard/Technitium). Only safe with a private/local upstream
	// you trust — Validate rejects this combined with a public/hostname address,
	// since it would otherwise leak client IPs off-network.
	ECS bool `yaml:"ecs_enabled,omitempty" json:"ecs_enabled,omitempty"`

	// MaxFails is the forward plugin's max_fails: how many consecutive failed
	// health checks mark Address down before CoreDNS starts routing around it
	// (CoreDNS default 2). nil means "unset, use the CoreDNS default" — a
	// pointer rather than a bare uint32 because 0 is itself a meaningful,
	// distinct setting (disable down-marking entirely, the recommended value
	// with a single local upstream that has no failover target anyway) and
	// must be distinguishable from "not configured".
	MaxFails *uint32 `yaml:"max_fails,omitempty" json:"max_fails,omitempty"`
	// HealthCheckSeconds is the forward plugin's health_check interval in
	// seconds (CoreDNS default 500ms when unset/0). Raising this reduces
	// background ". IN NS" probe traffic hitting Address.
	HealthCheckSeconds uint32 `yaml:"health_check_seconds,omitempty" json:"health_check_seconds,omitempty"`
	// ExpireSeconds is the forward plugin's expire: how long an idle
	// connection to Address is kept pooled before closing (CoreDNS default
	// 10s when unset/0).
	ExpireSeconds uint32 `yaml:"expire_seconds,omitempty" json:"expire_seconds,omitempty"`
	// PreferUDP makes the forward plugin try UDP to Address first even when
	// the client's own query arrived over TCP, retrying over TCP only if the
	// UDP reply comes back truncated. Avoids piling up one new TCP connection
	// per query against a local upstream that handles concurrent TCP poorly.
	PreferUDP bool `yaml:"prefer_udp,omitempty" json:"prefer_udp,omitempty"`
	// MaxConcurrent is the forward plugin's max_concurrent: a hard cap on
	// in-flight forwarded queries, past which new ones are REFUSEd instead of
	// queuing against an overloaded Address (0 = no cap, CoreDNS default).
	MaxConcurrent uint32 `yaml:"max_concurrent,omitempty" json:"max_concurrent,omitempty"`
}

// Listeners is the set of DNS transports this server terminates.
type Listeners struct {
	Plain Listener     `yaml:"plain" json:"plain"`
	DoT   Listener     `yaml:"dot" json:"dot"`
	DoH   Listener     `yaml:"doh" json:"doh"`
	DoQ   QUICListener `yaml:"doq" json:"doq"`
}

type Listener struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	Port    int  `yaml:"port" json:"port"`
}

// QUICListener is the DoQ listener plus its transport-specific tuning knobs
// (CoreDNS's "quic" plugin directive, RFC 9250). Zero means "not set" — the
// quic{} directive is then omitted entirely and CoreDNS's own defaults apply,
// rather than the settings model forcing a specific value on every install.
type QUICListener struct {
	Listener `yaml:",inline" json:",inline"`
	// MaxStreams caps simultaneous streams per QUIC connection.
	MaxStreams int `yaml:"max_streams,omitempty" json:"max_streams,omitempty"`
	// WorkerPoolSize caps the goroutine pool quic-go uses to service streams.
	WorkerPoolSize int `yaml:"worker_pool_size,omitempty" json:"worker_pool_size,omitempty"`
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
	// ServiceTypes to browse (e.g. "_http._tcp"). Three sentinel forms:
	// empty means browse nothing; ["default"] uses the plugin's curated
	// common-types list; ["all"] discovers types dynamically via the DNS-SD
	// meta-query instead of a fixed list. Anything else is browsed as an
	// explicit list of exactly those types.
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
