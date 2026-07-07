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
)

// Record is one declarative entry. Most records have just a Default value; a
// record may instead (or additionally) carry per-VLAN overrides for split-horizon
// answers (Phase 3). A record with no Default and no override for the querying
// VLAN yields NXDOMAIN for that VLAN.
type Record struct {
	Name string     `yaml:"name" json:"name"`
	Type RecordType `yaml:"type" json:"type"`
	// Default is the value returned when no per-VLAN override matches. For A/AAAA
	// it is an IP; for CNAME it is a target name. May be empty only if every
	// relevant VLAN is covered by an override.
	Default       string         `yaml:"default" json:"default"`
	VLANOverrides []VLANOverride `yaml:"vlan_overrides,omitempty" json:"vlan_overrides,omitempty"`
}

// VLANOverride ties a per-VLAN answer to a record (Phase 3). A nil Value is a
// deliberate "this VLAN gets NXDOMAIN for this name" — distinct from omitting
// the VLAN entirely (which falls back to Default). In YAML this is `value: null`.
type VLANOverride struct {
	VLAN  string  `yaml:"vlan" json:"vlan"`
	Value *string `yaml:"value" json:"value"`
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
