package localrecords

// The wire* types are the JSON contract with configgen (which marshals from the
// model package). The plugin deliberately does NOT import model — it re-declares
// the shape here so it depends only on coredns + miekg/dns and stays extractable
// (docs/repo-layout.md). Field names must match configgen's zoneData JSON tags.
type wireZone struct {
	DefaultTTL uint32       `json:"default_ttl"`
	Zones      []string     `json:"zones"`
	Records    []wireRecord `json:"records"`
	// DDR carries the Discovery of Designated Resolvers (RFC 9462) SVCB
	// records for _dns.resolver.arpa, synthesized by configgen from
	// acme.domain + the enabled encrypted listeners — never user-authored.
	DDR []wireDDR `json:"ddr,omitempty"`
}

type wireRecord struct {
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	Default       string         `json:"default"`
	TTL           uint32         `json:"ttl"`
	VLANOverrides []wireOverride `json:"vlan_overrides"`
}

type wireOverride struct {
	VLAN     string `json:"vlan"`
	Value    string `json:"value"`
	TTL      uint32 `json:"ttl"`
	NXDomain bool   `json:"nxdomain"`
}

type wireDDR struct {
	Priority uint16         `json:"priority"`
	Target   string         `json:"target"`
	Params   []wireDDRParam `json:"params"`
}

// wireDDRParam is one SvcParam key=value pair (e.g. alpn=dot, port=853,
// dohpath=/dns-query{?dns}) — RFC 9460 §7 lists the registered keys this
// covers (alpn, port, dohpath are all we ever emit).
type wireDDRParam struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
