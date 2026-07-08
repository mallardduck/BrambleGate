package model

import "strings"

// RecordSet mirrors /config/records.yaml (docs/config-schema.md).
//
// Struct tags carry both yaml (store, on disk) and json (GUI API, over the wire)
// so the same shapes round-trip through both without a translation layer.
type RecordSet struct {
	Records []Record `yaml:"records" json:"records"`
}

// RecordType is a supported DNS record type. Kept as a small closed set —
// localrecords is authoritative, not a general zone server.
type RecordType string

const (
	TypeA     RecordType = "A"
	TypeAAAA  RecordType = "AAAA"
	TypeCNAME RecordType = "CNAME"
	// TypeMDNS is a live record: its value is resolved from the mDNS discovery
	// table at query time via Match (empty when the device is absent), not stored.
	TypeMDNS RecordType = "mdns"
)

// Record is one declarative entry. Most records have just a Default value; a
// record may instead (or additionally) carry per-VLAN overrides for split-horizon
// answers (Phase 3). A record with no Default and no override for the querying
// VLAN yields NXDOMAIN for that VLAN.
type Record struct {
	Name string     `yaml:"name" json:"name"`
	Type RecordType `yaml:"type" json:"type"`
	// Default is the base value: an IP for A/AAAA, a target name for CNAME. It may
	// be empty only if every VLAN that should resolve this name is covered by an
	// override that supplies its own value.
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// TTL in seconds. 0 means "use the server default" (DefaultTTL). A per-VLAN
	// override may narrow this further.
	TTL           uint32         `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	VLANOverrides []VLANOverride `yaml:"vlan_overrides,omitempty" json:"vlan_overrides,omitempty"`
	// Match is set only for TypeMDNS records: the selector that resolves this name
	// against the live mDNS table. Default/TTL/VLANOverrides are unused then.
	Match *Selector `yaml:"match,omitempty" json:"match,omitempty"`
}

// IsMDNS reports whether this is a live mDNS-linked record.
func (r Record) IsMDNS() bool { return r.Type == TypeMDNS }

// VLANOverride adjusts the answer for a record when the client's source address
// falls in the named VLAN. It is a partial override of the base record:
//
//   - NXDomain true  → this VLAN gets no answer for this name (authoritative miss).
//   - Value non-empty → this VLAN gets Value instead of Record.Default.
//   - Value empty     → this VLAN inherits Record.Default (e.g. a TTL-only override).
//   - TTL non-zero    → this VLAN uses TTL; 0 inherits the record's effective TTL.
//
// NXDomain is mutually exclusive with Value/TTL (configgen enforces this).
type VLANOverride struct {
	VLAN     string `yaml:"vlan" json:"vlan"`
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`
	TTL      uint32 `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	NXDomain bool   `yaml:"nxdomain,omitempty" json:"nxdomain,omitempty"`
}

// NormalizedName returns the record name as a fully-qualified, lower-cased DNS
// name (trailing dot), the form used for matching against query names.
func (r Record) NormalizedName() string {
	n := strings.ToLower(strings.TrimSpace(r.Name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}
