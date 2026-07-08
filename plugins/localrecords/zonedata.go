package localrecords

// The wire* types are the JSON contract with configgen (which marshals from the
// model package). The plugin deliberately does NOT import model — it re-declares
// the shape here so it depends only on coredns + miekg/dns and stays extractable
// (docs/repo-layout.md). Field names must match configgen's zoneData JSON tags.
type wireZone struct {
	DefaultTTL uint32       `json:"default_ttl"`
	Zones      []string     `json:"zones"`
	VLANs      []wireVLAN   `json:"vlans"`
	Records    []wireRecord `json:"records"`
}

type wireVLAN struct {
	Name  string   `json:"name"`
	CIDRs []string `json:"cidrs"`
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
