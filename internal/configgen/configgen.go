// Package configgen validates the model and renders it into (a) Corefile text for
// engine.Reload and (b) a JSON zone-data file the localrecords plugin loads at
// setup. records.yaml is the source of truth; both outputs are its derived
// runtime form (docs/config-schema.md, docs/plugins.md). The one exception is
// acmeSelfRecords: an A/AAAA record for the ACME domain synthesized at render
// time from detected local IPs, never written to records.yaml (dev-docs/certificates.md).
//
// Why a JSON side-file instead of inlining records in the Corefile: per-VLAN
// overrides (value/ttl/nxdomain) and multi-CIDR VLANs are a structured, nested
// shape that CoreDNS's flat Corefile grammar does not express cleanly. The
// Corefile just points localrecords at the JSON path; the JSON carries the data.
package configgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mallardduck/BrambleGate/internal/configgen/corefile"
	"github.com/mallardduck/BrambleGate/internal/configgen/selfip"
	"github.com/mallardduck/BrambleGate/model"
)

// OwnedZone is the zone localrecords is fully authoritative for (NXDOMAIN on any
// miss — nothing under it should ever leak upstream).
const OwnedZone = "home.arpa"

// DDRZone is the reserved special-use domain Discovery of Designated Resolvers
// (RFC 9462) queries live under. Unlike the ACME domain, nothing should ever
// fall through here — it isn't delegated to anyone upstream — so a miss is
// NXDOMAIN, same as OwnedZone. Only added to the served zone list when there's
// at least one DDR record to answer with (see ddrRecords).
const DDRZone = "resolver.arpa"

// ownedZones returns every zone localrecords should serve, in the order
// they're written to the Corefile line: OwnedZone first, then the ACME domain
// when enabled/configured, then DDRZone when there's DDR data to serve. See
// fallthroughZones for which of these defer to Next on a miss rather than
// answering NXDOMAIN/NODATA.
func ownedZones(s model.Settings) []string {
	zones := []string{OwnedZone}
	if acmeDomain := normalizedACMEDomain(s); acmeDomain != "" {
		zones = append(zones, acmeDomain)
	}
	if len(ddrRecords(s)) > 0 {
		zones = append(zones, DDRZone)
	}
	return zones
}

// fallthroughZones is the subset of ownedZones where a miss defers to Next
// instead of answering NXDOMAIN/NODATA — just the ACME domain, which stays
// real/public-DNS-authoritative for anything not explicitly declared locally
// (e.g. the ACME hostname's own A/AAAA, so a device doesn't need it added to
// public DNS just to reach a LAN IP).
func fallthroughZones(s model.Settings) []string {
	if acmeDomain := normalizedACMEDomain(s); acmeDomain != "" {
		return []string{acmeDomain}
	}
	return nil
}

// normalizedACMEDomain returns the lower-cased, trimmed ACME domain when ACME
// is enabled and a domain is set, else "".
func normalizedACMEDomain(s model.Settings) string {
	if !s.ACME.Enabled {
		return ""
	}
	d := strings.ToLower(strings.TrimSpace(s.ACME.Domain))
	return d
}

// acmeSelfRecords returns render-time-only A/AAAA records for the ACME domain
// synthesized from detected local IPs (selfip) — never persisted to
// records.yaml (see ownedZones and dev-docs/certificates.md). Only
// (name,type) combinations the user hasn't already declared in declared are
// added; an explicit user record always wins. Returns nil when ACME is off,
// Domain is unset, or detection found nothing usable.
func acmeSelfRecords(s model.Settings, declared []model.Record, ips selfip.Result) []model.Record {
	name := normalizedACMEDomain(s)
	if name == "" {
		return nil
	}
	normalized := (model.Record{Name: name}).NormalizedName()
	declaredHas := func(t model.RecordType) bool {
		for _, r := range declared {
			if r.NormalizedName() == normalized && r.Type == t {
				return true
			}
		}
		return false
	}

	var out []model.Record
	if rec, ok := buildFamilyRecord(name, model.TypeA, ips, true); ok && !declaredHas(model.TypeA) {
		out = append(out, rec)
	}
	if rec, ok := buildFamilyRecord(name, model.TypeAAAA, ips, false); ok && !declaredHas(model.TypeAAAA) {
		out = append(out, rec)
	}
	return out
}

// PreviewACMESelfRecords exposes acmeSelfRecords for read-only display (the
// GUI dashboard's "auto-detected address" panel) without requiring a full
// Render/Validate pass.
func PreviewACMESelfRecords(s model.Settings, declared []model.Record, ips selfip.Result) []model.Record {
	return acmeSelfRecords(s, declared, ips)
}

// hasAddressRecord reports whether records contains an A or AAAA record for
// name (case/trailing-dot insensitive) that actually answers for at least one
// client — a plain Default, or a VLANOverride with a value (an nxdomain-only
// override doesn't count).
func hasAddressRecord(records []model.Record, name string) bool {
	normalized := (model.Record{Name: name}).NormalizedName()
	for _, r := range records {
		if r.Type != model.TypeA && r.Type != model.TypeAAAA {
			continue
		}
		if r.NormalizedName() != normalized {
			continue
		}
		if r.Default != "" {
			return true
		}
		for _, ov := range r.VLANOverrides {
			if ov.Value != "" {
				return true
			}
		}
	}
	return false
}

// buildFamilyRecord assembles one A or AAAA record from a selfip.Result: the
// Default is the primary fallback for that family, and each VLAN with a
// detected address of that family becomes a VLANOverride — so the record
// answers per client-source-VLAN exactly like a hand-written record would. ok
// is false when there is no usable value at all (no Default and no VLAN
// override), mirroring validate.go's "no default and no override" rule,
// enforced here directly since this record never goes through Validate.
func buildFamilyRecord(name string, t model.RecordType, ips selfip.Result, v4 bool) (model.Record, bool) {
	pick := func(va selfip.VLANAddrs) string {
		if v4 {
			return va.V4
		}
		return va.V6
	}

	rec := model.Record{Name: name, Type: t, Default: pick(ips.Primary)}
	for _, vlan := range sortedVLANNames(ips.PerVLAN) {
		if v := pick(ips.PerVLAN[vlan]); v != "" {
			rec.VLANOverrides = append(rec.VLANOverrides, model.VLANOverride{VLAN: vlan, Value: v})
		}
	}
	if rec.Default == "" && len(rec.VLANOverrides) == 0 {
		return model.Record{}, false
	}
	return rec, true
}

// sortedVLANNames returns m's keys sorted, so the synthesized record's
// VLANOverrides order is deterministic (map iteration order is not).
func sortedVLANNames(m map[string]selfip.VLANAddrs) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ddrRecord/ddrParam mirror the localrecords plugin's wireDDR/wireDDRParam
// JSON shape (field names must match, same convention as zoneData/model.Record
// above — see plugins/localrecords/zonedata.go).
type ddrRecord struct {
	Priority uint16     `json:"priority"`
	Target   string     `json:"target"`
	Params   []ddrParam `json:"params"`
}

type ddrParam struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ddrRecords returns the Discovery of Designated Resolvers (RFC 9462) SVCB
// records for _dns.resolver.arpa — one per enabled encrypted listener, so
// each can carry its own port (DoT/DoH/DoQ may each run on a different port).
// TargetName is the ACME domain: RFC 9462 requires an authentication domain
// name here, not "." — and it must be the name the issued certificate covers,
// since that's what the client validates when it upgrades (dev-docs/certificates.md).
// Entirely derived from settings — never user-authored, same rationale as
// acmeSelfRecords. Returns nil when ACME is off/unconfigured or no encrypted
// listener is enabled (nothing to designate).
func ddrRecords(s model.Settings) []ddrRecord {
	domain := normalizedACMEDomain(s)
	if domain == "" {
		return nil
	}
	var out []ddrRecord
	if s.Listeners.DoT.Enabled {
		out = append(out, ddrRecord{Priority: 1, Target: domain, Params: []ddrParam{
			{Key: "alpn", Value: "dot"},
			{Key: "port", Value: strconv.Itoa(s.Listeners.DoT.Port)},
		}})
	}
	if s.Listeners.DoH.Enabled {
		out = append(out, ddrRecord{Priority: 1, Target: domain, Params: []ddrParam{
			{Key: "alpn", Value: "h2"},
			{Key: "port", Value: strconv.Itoa(s.Listeners.DoH.Port)},
			{Key: "dohpath", Value: "/dns-query{?dns}"},
		}})
	}
	if s.Listeners.DoQ.Enabled {
		out = append(out, ddrRecord{Priority: 1, Target: domain, Params: []ddrParam{
			{Key: "alpn", Value: "doq"},
			{Key: "port", Value: strconv.Itoa(s.Listeners.DoQ.Port)},
		}})
	}
	if s.Listeners.DoH3.Enabled {
		out = append(out, ddrRecord{Priority: 1, Target: domain, Params: []ddrParam{
			{Key: "alpn", Value: "h3"},
			{Key: "port", Value: strconv.Itoa(s.Listeners.DoH3.Port)},
			{Key: "dohpath", Value: "/dns-query{?dns}"},
		}})
	}
	return out
}

// DefaultTTL is the server-wide fallback TTL for records that set none.
const DefaultTTL = 300

// Options carries render-time inputs that are not part of the persisted model.
type Options struct {
	// ConfigDir is the /config root; used to compute the zone-data path that the
	// rendered Corefile points localrecords at.
	ConfigDir string
	// CertFile/KeyFile back the tls directive for encrypted listeners.
	CertFile string
	KeyFile  string
	// ACMESelfIPs is the caller's freshly-detected local IPs per VLAN (see
	// selfip), used to synthesize an A/AAAA record for acme.domain
	// when the user hasn't declared one explicitly. A zero value means
	// "nothing detected" — Render then adds no synthetic record, the same
	// outcome as a bridge-mode deployment.
	ACMESelfIPs selfip.Result
}

// Rendered is the output of Render: the Corefile bytes for the engine, and the
// JSON zone-data bytes that must be written to ZoneDataPath(ConfigDir) before the
// engine loads/reloads (the localrecords plugin reads it at setup).
type Rendered struct {
	Corefile []byte
	ZoneData []byte
}

// zoneData is the wire contract between configgen (writer) and the localrecords
// plugin (reader). The plugin declares a matching struct — they agree on JSON
// field names, not Go types, so the plugin stays free of a dependency on model.
type zoneData struct {
	DefaultTTL uint32         `json:"default_ttl"`
	Zones      []string       `json:"zones"`
	Records    []model.Record `json:"records"`
	DDR        []ddrRecord    `json:"ddr,omitempty"`
}

// Render validates the model and returns the Corefile plus JSON zone data. On any
// validation failure it returns an error and no output — configgen fails loudly
// here rather than handing CoreDNS an invalid config (docs/config-schema.md).
func Render(s model.Settings, rs model.RecordSet, opts Options) (Rendered, error) {
	if err := Validate(s, rs); err != nil {
		return Rendered{}, err
	}

	// mdns-typed records are served live by the mdnsbridge plugin, not localrecords,
	// so they are excluded from the zone data (which has no static value for them).
	static := make([]model.Record, 0, len(rs.Records))
	for _, r := range rs.Records {
		if !r.IsMDNS() {
			static = append(static, r)
		}
	}
	static = append(static, acmeSelfRecords(s, rs.Records, opts.ACMESelfIPs)...)

	ddr := ddrRecords(s)
	if len(ddr) > 0 && !hasAddressRecord(static, ddr[0].Target) {
		return Rendered{}, fmt.Errorf("ddr: an encrypted listener is enabled with acme domain %q but no A/AAAA record resolves for it (no VLAN address detected and none declared) — the DDR SVCB record would point at a name that doesn't resolve", ddr[0].Target)
	}

	zone, err := json.MarshalIndent(zoneData{
		DefaultTTL: DefaultTTL,
		Zones:      ownedZones(s),
		Records:    static,
		DDR:        ddrRecords(s),
	}, "", "  ")
	if err != nil {
		return Rendered{}, fmt.Errorf("marshal zone data: %w", err)
	}

	return Rendered{Corefile: buildCorefile(s, opts), ZoneData: zone}, nil
}

func buildCorefile(s model.Settings, opts Options) []byte {
	var out strings.Builder
	// health/ready/prometheus are process-wide singletons: CoreDNS errors on
	// the same directive appearing in more than one server block, so they're
	// only emitted into whichever listener below is enabled first, in this
	// fixed Plain/DoT/DoH/DoQ/DoH3 order.
	observability := true
	nextBlock := func(addr string, tls bool, quic *model.QUICListener, quicPluginName string) string {
		blk := buildServerBlock(addr, tls, quic, quicPluginName, s, opts, observability)
		observability = false
		return blk
	}
	if s.Listeners.Plain.Enabled {
		out.WriteString(nextBlock(fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false, nil, ""))
	}
	if s.Listeners.DoT.Enabled {
		out.WriteString(nextBlock(fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true, nil, ""))
	}
	if s.Listeners.DoH.Enabled {
		out.WriteString(nextBlock(fmt.Sprintf("https://.:%d", s.Listeners.DoH.Port), true, nil, ""))
	}
	if s.Listeners.DoQ.Enabled {
		q := s.Listeners.DoQ
		out.WriteString(nextBlock(fmt.Sprintf("quic://.:%d", q.Port), true, &q, "quic"))
	}
	if s.Listeners.DoH3.Enabled {
		q := s.Listeners.DoH3
		out.WriteString(nextBlock(fmt.Sprintf("https3://.:%d", q.Port), true, &q, "https3"))
	}
	return []byte(out.String())
}

// buildServerBlock renders one Corefile server block. quic is non-nil for the
// DoQ and DoH3 blocks (quicPluginName distinguishes which tuning sub-block
// header to emit, "quic" or "https3" — both take max_streams, only "quic"
// takes worker_pool_size); its tuning sub-block is itself only rendered when
// at least one of MaxStreams/WorkerPoolSize is set — 0 isn't a valid value
// for either (both plugins reject <= 0), so 0 means "leave it to CoreDNS's
// own default" rather than being written as a literal 0. observability is
// true only for the one server block (buildCorefile's first enabled listener)
// that should carry the health/ready/prometheus directives.
func buildServerBlock(addr string, tls bool, quic *model.QUICListener, quicPluginName string, s model.Settings, opts Options, observability bool) string {
	blk := corefile.NewBlock(addr)
	if tls {
		blk.Directive("tls %s %s", opts.CertFile, opts.KeyFile)
	}
	if quic != nil && (quic.MaxStreams > 0 || quic.WorkerPoolSize > 0) {
		blk.SubBlock(quicPluginName, func(inner *corefile.Block) {
			inner.DirectiveIf(quic.MaxStreams > 0, "max_streams %d", quic.MaxStreams)
			inner.DirectiveIf(quic.WorkerPoolSize > 0, "worker_pool_size %d", quic.WorkerPoolSize)
		})
	}
	// timeouts is a fixed idle-timeout bump on encrypted listeners only (DoT/DoH/DoQ,
	// i.e. wherever tls is true) — helps DoT/DoH clients behind NAT/routers that hold
	// connections open, and mobile clients moving between networks. Plain UDP/TCP has
	// no persistent connection to tune, so it's left at CoreDNS's own defaults.
	if tls {
		blk.SubBlock("timeouts", func(inner *corefile.Block) {
			inner.Directive("idle 3m")
		})
	}
	blk.DirectiveIf(!s.BufsizeDisabled, "bufsize 1232")
	if observability {
		writeObservability(blk, s)
	}
	// mdnsbridge (argument-free; reads the process-owned discovery table) runs
	// ahead of localrecords per the directive order. Only rendered when enabled.
	blk.DirectiveIf(s.MDNS.Enabled, "mdnsbridge")

	zones := ownedZones(s)
	blk.SubBlock("localrecords "+strings.Join(zones, " "), func(inner *corefile.Block) {
		inner.Directive("zonedata %s", ZoneDataPath(opts.ConfigDir))
		if ft := fallthroughZones(s); len(ft) > 0 {
			inner.Directive("fallthrough %s", strings.Join(ft, " "))
		}
	})

	// ecs_enabled attaches the real client source IP to the forwarded query via
	// EDNS0 Client Subnet (RFC 7871), so the upstream can apply per-client policy.
	// Validate rejects this unless the upstream is private/loopback (docs/plugins.md),
	// so full-precision masks (32/128, i.e. no truncation) are safe here.
	blk.DirectiveIf(s.UpstreamDNS.ECS, "rewrite edns0 subnet set 32 128")
	writeForward(blk, s.UpstreamDNS)
	writeCache(blk, s)
	writeErrors(blk, s)
	writeLog(blk, s)
	return blk.String()
}

// writeObservability renders health/ready/prometheus, each off by default
// and each on a fixed, non-tunable address (model.Observability's doc
// comment explains why). Called by buildServerBlock for at most one server
// block per Corefile — see buildCorefile.
func writeObservability(blk *corefile.Block, s model.Settings) {
	o := s.Observability
	blk.DirectiveIf(o.Health, "health :9090")
	blk.DirectiveIf(o.Ready, "ready :9191")
	blk.DirectiveIf(o.Prometheus, "prometheus :9153")
}

// writeCache renders the cache directive, or omits it entirely: cache has no
// concept of client subnet — it keys purely on qname/qtype/qclass
// (plugin/cache) — so with ecs_enabled on it would cache one client's
// subnet-scoped upstream answer and serve it to every other client regardless
// of their real subnet, defeating ECS the same way caching a split-horizon
// localrecords answer would (see localrecords/mdnsbridge's own cache-bypass
// ordering above). That correctness rule always wins, before s.Cache's own
// tuning is even considered.
func writeCache(blk *corefile.Block, s model.Settings) {
	if s.UpstreamDNS.ECS {
		return
	}
	c := s.Cache
	if c.ServeStaleDisabled && c.PrefetchDisabled {
		blk.Directive("cache")
		return
	}
	blk.SubBlock("cache", func(inner *corefile.Block) {
		inner.DirectiveIf(!c.PrefetchDisabled, "prefetch 10 1m 10%%")
		inner.DirectiveIf(!c.ServeStaleDisabled, "serve_stale 1h immediate")
	})
}

// writeErrors renders the errors directive with consolidate on by default —
// collapsing repeated identical failures (a flaky upstream timing out) into a
// periodic summary instead of one line per error — or bare when disabled.
// The window/pattern/level are the plugin's own documented example, not
// user-tunable (see model.ErrorsTuning).
func writeErrors(blk *corefile.Block, s model.Settings) {
	if s.Errors.ConsolidateDisabled {
		blk.Directive("errors")
		return
	}
	blk.SubBlock("errors", func(inner *corefile.Block) {
		inner.Directive(`consolidate 5m ".* i/o timeout$" warning`)
	})
}

// writeLog renders the log directive, or omits it entirely when disabled, or
// scopes it to specific response classes when set. Default (both fields zero)
// matches CoreDNS's own unconditional
// log-everything behavior, unchanged from before these settings existed.
func writeLog(blk *corefile.Block, s model.Settings) {
	if s.Log.Disabled {
		return
	}
	if len(s.Log.Classes) == 0 {
		blk.Directive("log")
		return
	}
	blk.SubBlock("log", func(inner *corefile.Block) {
		inner.Directive("class %s", strings.Join(s.Log.Classes, " "))
	})
}

// writeForward renders the forward directive. When none of UpstreamTarget's
// forward-tuning fields are set, it's a bare "forward . <target>" line and
// CoreDNS's own defaults apply untouched; otherwise it opens a tuning
// sub-block with only the explicitly-set knobs (docs/plugins.md).
func writeForward(blk *corefile.Block, u model.UpstreamTarget) {
	target := forwardTarget(u)
	if u.MaxFails == nil && u.HealthCheckSeconds == 0 && u.ExpireSeconds == 0 && !u.PreferUDP && u.MaxConcurrent == 0 {
		blk.Directive("forward . %s", target)
		return
	}
	blk.SubBlock("forward . "+target, func(inner *corefile.Block) {
		if u.MaxFails != nil {
			inner.Directive("max_fails %d", *u.MaxFails)
		}
		inner.DirectiveIf(u.HealthCheckSeconds > 0, "health_check %ds", u.HealthCheckSeconds)
		inner.DirectiveIf(u.ExpireSeconds > 0, "expire %ds", u.ExpireSeconds)
		inner.DirectiveIf(u.PreferUDP, "prefer_udp")
		inner.DirectiveIf(u.MaxConcurrent > 0, "max_concurrent %d", u.MaxConcurrent)
	})
}

// forwardTarget renders the upstream for the forward plugin, honoring an
// encrypted internal hop when the upstream protocol is dot/doh.
func forwardTarget(u model.UpstreamTarget) string {
	switch u.Protocol {
	case "dot":
		return "tls://" + u.Address
	case "doh":
		return "https://" + u.Address
	default:
		return u.Address
	}
}
