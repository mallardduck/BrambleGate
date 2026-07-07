// Package configgen validates the model and renders it into Corefile text for
// engine.Reload. The localrecords records are emitted inline in the Corefile, so
// the rendered bytes are the single, self-contained unit the engine consumes —
// records.yaml is the source of truth, this is its derived runtime form
// (docs/config-schema.md, docs/plugins.md).
package configgen

import (
	"fmt"
	"strings"

	"github.com/mallardduck/BrambleDNS/model"
)

// OwnedZone is the zone localrecords is authoritative for. Configurable owned
// subdomains can be added later; for now everything hangs under home.arpa.
const OwnedZone = "home.arpa"

// DefaultTTL is the TTL rendered into the localrecords block.
const DefaultTTL = 300

// Options carries render-time inputs that are not part of the persisted model —
// currently the certificate paths for the encrypted listeners.
type Options struct {
	CertFile string
	KeyFile  string
}

// Render validates the model and returns the Corefile bytes. On any validation
// failure it returns an error and no bytes — configgen fails loudly here rather
// than handing CoreDNS an invalid Corefile (docs/config-schema.md).
func Render(s model.Settings, rs model.RecordSet, opts Options) ([]byte, error) {
	if err := Validate(s, rs); err != nil {
		return nil, err
	}

	var b strings.Builder
	if s.Listeners.Plain.Enabled {
		writeServerBlock(&b, fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false, s, rs, opts)
	}
	if s.Listeners.DoT.Enabled {
		writeServerBlock(&b, fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true, s, rs, opts)
	}
	return []byte(b.String()), nil
}

func writeServerBlock(b *strings.Builder, addr string, tls bool, s model.Settings, rs model.RecordSet, opts Options) {
	fmt.Fprintf(b, "%s {\n", addr)
	if tls {
		fmt.Fprintf(b, "\ttls %s %s\n", opts.CertFile, opts.KeyFile)
	}
	writeLocalRecords(b, rs)
	fmt.Fprintf(b, "\tforward . %s\n", forwardTarget(s.UpstreamDNS))
	b.WriteString("\tcache\n")
	b.WriteString("\terrors\n")
	b.WriteString("\tlog\n")
	b.WriteString("}\n")
}

// writeLocalRecords emits the localrecords stanza. Phase 2 renders each record's
// Default value; per-VLAN overrides (Record.VLANOverrides) are rendered in Phase 3.
func writeLocalRecords(b *strings.Builder, rs model.RecordSet) {
	fmt.Fprintf(b, "\tlocalrecords %s {\n", OwnedZone)
	fmt.Fprintf(b, "\t\tttl %d\n", DefaultTTL)
	for _, r := range rs.Records {
		if r.Default == "" {
			continue // no default value to serve yet (override-only records: Phase 3)
		}
		name := strings.TrimSuffix(r.NormalizedName(), ".")
		fmt.Fprintf(b, "\t\trecord %s %s %s\n", name, r.Type, r.Default)
	}
	b.WriteString("\t}\n")
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
