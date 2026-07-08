package configgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleDNS/model"
)

func baseSettings() model.Settings {
	return model.Settings{
		VLANs: []model.VLAN{
			{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
			{Name: "untrusted-wifi", CIDRs: []string{"192.168.30.0/24"}},
		},
		UpstreamDNS: model.UpstreamTarget{Address: "192.168.10.5:53", Protocol: "plain"},
		Listeners: model.Listeners{
			Plain: model.Listener{Enabled: true, Port: 53},
			DoT:   model.Listener{Enabled: true, Port: 853},
		},
		ACME: model.ACME{Domain: "dns.example.com"},
	}
}

func TestRenderCorefilePointsAtZoneData(t *testing.T) {
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"},
	}}
	out, err := Render(baseSettings(), rs, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	for _, want := range []string{
		".:53 {",
		"tls://.:853 {",
		"tls /c/cert.pem /c/key.pem",
		"localrecords home.arpa {",
		"zonedata " + ZoneDataPath("/config"),
		"forward . 192.168.10.5:53",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Corefile missing %q:\n%s", want, cf)
		}
	}
	if strings.Count(cf, "localrecords home.arpa {") != 2 {
		t.Errorf("localrecords should appear in both server blocks:\n%s", cf)
	}
}

func TestRenderZoneDataJSON(t *testing.T) {
	deny := model.VLANOverride{VLAN: "untrusted-wifi", NXDomain: true}
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20", TTL: 120, VLANOverrides: []model.VLANOverride{deny}},
	}}
	out, err := Render(baseSettings(), rs, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var zd struct {
		DefaultTTL uint32         `json:"default_ttl"`
		Zones      []string       `json:"zones"`
		VLANs      []model.VLAN   `json:"vlans"`
		Records    []model.Record `json:"records"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	if zd.DefaultTTL != DefaultTTL || len(zd.Zones) != 1 || zd.Zones[0] != OwnedZone {
		t.Fatalf("unexpected zone header: %+v", zd)
	}
	if len(zd.VLANs) != 2 || zd.VLANs[0].CIDRs[0] != "192.168.10.0/24" {
		t.Fatalf("vlans not carried into zone data: %+v", zd.VLANs)
	}
	if len(zd.Records) != 1 || zd.Records[0].TTL != 120 || !zd.Records[0].VLANOverrides[0].NXDomain {
		t.Fatalf("record/override not carried into zone data: %+v", zd.Records)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func() (model.Settings, model.RecordSet){
		"overlapping vlan cidrs": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.VLANs = []model.VLAN{
				{Name: "a", CIDRs: []string{"192.168.0.0/16"}},
				{Name: "b", CIDRs: []string{"192.168.10.0/24"}},
			}
			return s, model.RecordSet{}
		},
		"vlan without cidr": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.VLANs = []model.VLAN{{Name: "empty"}}
			return s, model.RecordSet{}
		},
		"bad upstream": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.UpstreamDNS.Address = "nope"
			return s, model.RecordSet{}
		},
		"record outside zone": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "evil.example.com", Type: model.TypeA, Default: "1.2.3.4"},
			}}
		},
		"bad A value": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, Default: "not-an-ip"},
			}}
		},
		"override unknown vlan": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, Default: "10.0.0.2", VLANOverrides: []model.VLANOverride{
					{VLAN: "ghost", Value: "10.0.0.1"},
				}},
			}}
		},
		"nxdomain with value": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, Default: "10.0.0.2", VLANOverrides: []model.VLANOverride{
					{VLAN: "trusted", NXDomain: true, Value: "10.0.0.9"},
				}},
			}}
		},
		"ttl-only override without default": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, VLANOverrides: []model.VLANOverride{
					{VLAN: "trusted", TTL: 30},
				}},
			}}
		},
		"noop override": func() (model.Settings, model.RecordSet) {
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, Default: "10.0.0.2", VLANOverrides: []model.VLANOverride{
					{VLAN: "trusted"},
				}},
			}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			s, rs := mk()
			if _, err := Render(s, rs, Options{}); err == nil {
				t.Fatalf("expected validation error for %q", name)
			}
		})
	}
}

func TestValidateAcceptsRichOverrides(t *testing.T) {
	s := baseSettings()
	s.VLANs = append(s.VLANs, model.VLAN{Name: "guests", CIDRs: []string{"192.168.40.0/24"}})
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20", TTL: 300, VLANOverrides: []model.VLANOverride{
			{VLAN: "untrusted-wifi", NXDomain: true},
			{VLAN: "guests", TTL: 30}, // ttl-only, inherits default
		}},
	}}
	if err := Validate(s, rs); err != nil {
		t.Fatalf("rich overrides should validate: %v", err)
	}
}
