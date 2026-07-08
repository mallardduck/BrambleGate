// Package configgen validates the model and renders it into (a) Corefile text for
// engine.Reload and (b) a JSON zone-data file the localrecords plugin loads at
// setup. records.yaml is the source of truth; both outputs are its derived
// runtime form (docs/config-schema.md, docs/plugins.md).
//
// Why a JSON side-file instead of inlining records in the Corefile: per-VLAN
// overrides (value/ttl/nxdomain) and multi-CIDR VLANs are a structured, nested
// shape that CoreDNS's flat Corefile grammar does not express cleanly. The
// Corefile just points localrecords at the JSON path; the JSON carries the data.
package configgen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mallardduck/BrambleDNS/model"
)

// OwnedZone is the zone localrecords is authoritative for. Configurable owned
// subdomains can be added later; for now everything hangs under home.arpa.
const OwnedZone = "home.arpa"

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

	zone, err := json.MarshalIndent(zoneData{
		DefaultTTL: DefaultTTL,
		Zones:      []string{OwnedZone},
		VLANs:      s.VLANs,
		Records:    rs.Records,
	}, "", "  ")
	if err != nil {
		return Rendered{}, fmt.Errorf("marshal zone data: %w", err)
	}

	return Rendered{Corefile: buildCorefile(s, opts), ZoneData: zone}, nil
}

func buildCorefile(s model.Settings, opts Options) []byte {
	var b strings.Builder
	if s.Listeners.Plain.Enabled {
		writeServerBlock(&b, fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false, s, opts)
	}
	if s.Listeners.DoT.Enabled {
		writeServerBlock(&b, fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true, s, opts)
	}
	return []byte(b.String())
}

func writeServerBlock(b *strings.Builder, addr string, tls bool, s model.Settings, opts Options) {
	fmt.Fprintf(b, "%s {\n", addr)
	if tls {
		fmt.Fprintf(b, "\ttls %s %s\n", opts.CertFile, opts.KeyFile)
	}
	fmt.Fprintf(b, "\tlocalrecords %s {\n", OwnedZone)
	fmt.Fprintf(b, "\t\tzonedata %s\n", ZoneDataPath(opts.ConfigDir))
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
