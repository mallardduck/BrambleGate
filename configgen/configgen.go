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

	"github.com/mallardduck/BrambleGate/configgen/corefile"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/selfip"
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
	VLANs      []model.VLAN   `json:"vlans"`
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

	zone, err := json.MarshalIndent(zoneData{
		DefaultTTL: DefaultTTL,
		Zones:      ownedZones(s),
		VLANs:      s.VLANs,
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
	if s.Listeners.Plain.Enabled {
		out.WriteString(buildServerBlock(fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false, nil, s, opts))
	}
	if s.Listeners.DoT.Enabled {
		out.WriteString(buildServerBlock(fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true, nil, s, opts))
	}
	if s.Listeners.DoH.Enabled {
		out.WriteString(buildServerBlock(fmt.Sprintf("https://.:%d", s.Listeners.DoH.Port), true, nil, s, opts))
	}
	if s.Listeners.DoQ.Enabled {
		q := s.Listeners.DoQ
		out.WriteString(buildServerBlock(fmt.Sprintf("quic://.:%d", q.Port), true, &q, s, opts))
	}
	return []byte(out.String())
}

// buildServerBlock renders one Corefile server block. quic is non-nil only
// for the DoQ block, and its quic{} tuning sub-block is itself only rendered
// when at least one of MaxStreams/WorkerPoolSize is set — 0 isn't a valid
// value for either (both plugins reject <= 0), so 0 means "leave it to
// CoreDNS's own default" rather than being written as a literal 0.
func buildServerBlock(addr string, tls bool, quic *model.QUICListener, s model.Settings, opts Options) string {
	blk := corefile.NewBlock(addr)
	if tls {
		blk.Directive("tls %s %s", opts.CertFile, opts.KeyFile)
	}
	if quic != nil && (quic.MaxStreams > 0 || quic.WorkerPoolSize > 0) {
		blk.SubBlock("quic", func(inner *corefile.Block) {
			inner.DirectiveIf(quic.MaxStreams > 0, "max_streams %d", quic.MaxStreams)
			inner.DirectiveIf(quic.WorkerPoolSize > 0, "worker_pool_size %d", quic.WorkerPoolSize)
		})
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
	blk.Directive("forward . %s", forwardTarget(s.UpstreamDNS))
	blk.Directive("cache")
	blk.Directive("errors")
	blk.Directive("log")
	return blk.String()
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
