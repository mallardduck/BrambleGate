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
	"strings"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/selfip"
)

// OwnedZone is the zone localrecords is fully authoritative for (NXDOMAIN on any
// miss — nothing under it should ever leak upstream).
const OwnedZone = "home.arpa"

// ownedZones returns every zone localrecords should serve, in the order they're
// written to the Corefile line — OwnedZone first, then the ACME domain (as a
// fallthrough zone; see writeServerBlock) when ACME is enabled and configured.
// The ACME domain stays real/public-DNS-authoritative for anything not
// explicitly declared here — only a locally-declared record (e.g. the ACME
// hostname's own A/AAAA, so a device doesn't need it added to public DNS just
// to reach a LAN IP) is answered locally.
func ownedZones(s model.Settings) []string {
	zones := []string{OwnedZone}
	if s.ACME.Enabled && strings.TrimSpace(s.ACME.Domain) != "" {
		zones = append(zones, strings.ToLower(strings.TrimSpace(s.ACME.Domain)))
	}
	return zones
}

// acmeSelfRecords returns render-time-only A/AAAA records for the ACME domain
// synthesized from detected local IPs (selfip) — never persisted to
// records.yaml (see ownedZones and dev-docs/certificates.md). Only
// (name,type) combinations the user hasn't already declared in declared are
// added; an explicit user record always wins. Returns nil when ACME is off,
// Domain is unset, or detection found nothing usable.
func acmeSelfRecords(s model.Settings, declared []model.Record, ips selfip.Result) []model.Record {
	if !s.ACME.Enabled || strings.TrimSpace(s.ACME.Domain) == "" {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(s.ACME.Domain))
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
	}, "", "  ")
	if err != nil {
		return Rendered{}, fmt.Errorf("marshal zone data: %w", err)
	}

	return Rendered{Corefile: buildCorefile(s, opts), ZoneData: zone}, nil
}

func buildCorefile(s model.Settings, opts Options) []byte {
	var b strings.Builder
	if s.Listeners.Plain.Enabled {
		writeServerBlock(&b, fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false, "", s, opts)
	}
	if s.Listeners.DoT.Enabled {
		writeServerBlock(&b, fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true, "", s, opts)
	}
	if s.Listeners.DoH.Enabled {
		writeServerBlock(&b, fmt.Sprintf("https://.:%d", s.Listeners.DoH.Port), true, "", s, opts)
	}
	if s.Listeners.DoQ.Enabled {
		writeServerBlock(&b, fmt.Sprintf("quic://.:%d", s.Listeners.DoQ.Port), true, quicDirective(s.Listeners.DoQ), s, opts)
	}
	return []byte(b.String())
}

// quicDirective renders the optional "quic" plugin tuning block for DoQ.
// Zero fields are omitted rather than written as literal 0s, since 0 isn't a
// valid max_streams/worker_pool_size (both plugins reject <= 0) — it means
// "leave this to CoreDNS's own default".
func quicDirective(l model.QUICListener) string {
	if l.MaxStreams == 0 && l.WorkerPoolSize == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\tquic {\n")
	if l.MaxStreams > 0 {
		fmt.Fprintf(&b, "\t\tmax_streams %d\n", l.MaxStreams)
	}
	if l.WorkerPoolSize > 0 {
		fmt.Fprintf(&b, "\t\tworker_pool_size %d\n", l.WorkerPoolSize)
	}
	b.WriteString("\t}\n")
	return b.String()
}

func writeServerBlock(b *strings.Builder, addr string, tls bool, extra string, s model.Settings, opts Options) {
	fmt.Fprintf(b, "%s {\n", addr)
	if tls {
		fmt.Fprintf(b, "\ttls %s %s\n", opts.CertFile, opts.KeyFile)
	}
	b.WriteString(extra)
	// mdnsbridge (argument-free; reads the process-owned discovery table) runs
	// ahead of localrecords per the directive order. Only rendered when enabled.
	if s.MDNS.Enabled {
		b.WriteString("\tmdnsbridge\n")
	}
	zones := ownedZones(s)
	fmt.Fprintf(b, "\tlocalrecords %s {\n", strings.Join(zones, " "))
	fmt.Fprintf(b, "\t\tzonedata %s\n", ZoneDataPath(opts.ConfigDir))
	if len(zones) > 1 {
		// Everything after OwnedZone (currently just the ACME domain, if set) is a
		// fallthrough zone — see ownedZones.
		fmt.Fprintf(b, "\t\tfallthrough %s\n", strings.Join(zones[1:], " "))
	}
	b.WriteString("\t}\n")
	fmt.Fprintf(b, "\tforward . %s\n", forwardTarget(s.UpstreamDNS))
	b.WriteString("\tcache\n")
	b.WriteString("\terrors\n")
	b.WriteString("\tlog\n")
	b.WriteString("}\n")
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
