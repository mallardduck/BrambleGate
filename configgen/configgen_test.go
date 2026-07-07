package configgen

import (
	"strings"
	"testing"

	"github.com/mallardduck/BrambleDNS/model"
)

func baseSettings() model.Settings {
	return model.Settings{
		VLANs:       []model.VLAN{{Name: "trusted", CIDR: "192.168.10.0/24"}},
		UpstreamDNS: model.UpstreamTarget{Address: "192.168.10.5:53", Protocol: "plain"},
		Listeners: model.Listeners{
			Plain: model.Listener{Enabled: true, Port: 53},
			DoT:   model.Listener{Enabled: true, Port: 853},
		},
		ACME: model.ACME{Domain: "dns.example.com"},
	}
}

func TestRenderIncludesRecordsAndForward(t *testing.T) {
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"},
		{Name: "git.home.arpa", Type: model.TypeCNAME, Default: "nas.home.arpa"},
	}}
	out, err := Render(baseSettings(), rs, Options{CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		".:53 {",
		"tls://.:853 {",
		"tls /c/cert.pem /c/key.pem",
		"localrecords home.arpa {",
		"record nas.home.arpa A 192.168.10.20",
		"record git.home.arpa CNAME nas.home.arpa",
		"forward . 192.168.10.5:53",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// localrecords + forward should appear in BOTH server blocks.
	if strings.Count(got, "localrecords home.arpa {") != 2 {
		t.Errorf("localrecords should appear in both blocks:\n%s", got)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func() (model.Settings, model.RecordSet){
		"overlapping vlans": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.VLANs = []model.VLAN{{Name: "a", CIDR: "192.168.0.0/16"}, {Name: "b", CIDR: "192.168.10.0/24"}}
			return s, model.RecordSet{}
		},
		"bad upstream": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.UpstreamDNS.Address = "nope"
			return s, model.RecordSet{}
		},
		"dot without acme domain": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.ACME.Domain = ""
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
			deny := "10.0.0.1"
			return baseSettings(), model.RecordSet{Records: []model.Record{
				{Name: "x.home.arpa", Type: model.TypeA, Default: "10.0.0.2", VLANOverrides: []model.VLANOverride{
					{VLAN: "ghost", Value: &deny},
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

func TestValidateAcceptsNullOverride(t *testing.T) {
	s := baseSettings()
	s.VLANs = append(s.VLANs, model.VLAN{Name: "untrusted-wifi", CIDR: "192.168.30.0/24"})
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20", VLANOverrides: []model.VLANOverride{
			{VLAN: "untrusted-wifi", Value: nil}, // explicit NXDOMAIN — valid
		}},
	}}
	if err := Validate(s, rs); err != nil {
		t.Fatalf("null override should validate: %v", err)
	}
}
