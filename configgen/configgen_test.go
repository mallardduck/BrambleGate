package configgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/selfip"
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

// acmeEnabledSettings returns baseSettings with ACME turned on and the other
// fields Validate requires when it's enabled (email, dns_provider) filled in.
func acmeEnabledSettings() model.Settings {
	s := baseSettings()
	s.ACME.Enabled = true
	s.ACME.Email = "admin@example.com"
	s.ACME.DNSProvider = "cloudflare"
	return s
}

// The onboarding defaults seeded on first run (model.DefaultSettings) must render
// a valid Corefile with no further input, otherwise a fresh container would fail
// to start before the operator has touched anything.
func TestDefaultSettingsRenderWithNoRecords(t *testing.T) {
	out, err := Render(model.DefaultSettings(), model.RecordSet{}, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("default settings must render cleanly: %v", err)
	}
	cf := string(out.Corefile)
	if !strings.Contains(cf, ".:53 {") || !strings.Contains(cf, "forward . 1.1.1.1:53") {
		t.Errorf("default Corefile missing plain listener/forward:\n%s", cf)
	}
	if strings.Contains(cf, "tls://") {
		t.Errorf("default settings should not enable an encrypted listener:\n%s", cf)
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

func TestRenderCorefileIncludesACMEDomainAsFallthroughZone(t *testing.T) {
	s := baseSettings()
	s.ACME.Enabled = true
	s.ACME.Email = "admin@example.com"
	s.ACME.DNSProvider = "cloudflare"
	rs := model.RecordSet{Records: []model.Record{
		{Name: "dns.example.com", Type: model.TypeA, Default: "192.168.10.53"},
	}}

	out, err := Render(s, rs, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	for _, want := range []string{
		// resolver.arpa is also present: baseSettings enables DoT, so a DDR
		// record exists too (see TestRenderIncludesDDRZoneAndFallthroughIsACMEDomainOnly).
		"localrecords home.arpa dns.example.com resolver.arpa {",
		"fallthrough dns.example.com",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Corefile missing %q:\n%s", want, cf)
		}
	}
}

func TestRenderIncludesDDRZoneAndFallthroughIsACMEDomainOnly(t *testing.T) {
	s := acmeEnabledSettings() // DoT enabled in baseSettings
	rs := model.RecordSet{Records: []model.Record{
		{Name: "dns.example.com", Type: model.TypeA, Default: "192.168.10.53"},
	}}
	out, err := Render(s, rs, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	if !strings.Contains(cf, "localrecords home.arpa dns.example.com resolver.arpa {") {
		t.Errorf("Corefile missing DDR zone in localrecords line:\n%s", cf)
	}
	// resolver.arpa must NOT be in the fallthrough line — a miss there is
	// NXDOMAIN, same as home.arpa, unlike the ACME domain.
	if strings.Contains(cf, "fallthrough dns.example.com resolver.arpa") || strings.Contains(cf, "fallthrough resolver.arpa") {
		t.Errorf("resolver.arpa must not be a fallthrough zone:\n%s", cf)
	}
	if !strings.Contains(cf, "fallthrough dns.example.com") {
		t.Errorf("Corefile missing ACME domain fallthrough:\n%s", cf)
	}
}

func TestValidateRejectsACMEDomainRecordWhenACMEDisabled(t *testing.T) {
	s := baseSettings() // ACME.Enabled is false
	rs := model.RecordSet{Records: []model.Record{
		{Name: "dns.example.com", Type: model.TypeA, Default: "192.168.10.53"},
	}}
	if err := Validate(s, rs); err == nil {
		t.Fatal("expected an error: dns.example.com is outside home.arpa while ACME is disabled")
	}
}

func TestRenderSynthesizesACMEAddressRecords(t *testing.T) {
	s := acmeEnabledSettings()
	ips := selfip.Result{
		PerVLAN: map[string]selfip.VLANAddrs{"trusted": {V4: "192.168.10.53"}},
		Primary: selfip.VLANAddrs{V4: "192.168.10.53"},
	}

	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config", ACMESelfIPs: ips})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var zd struct {
		Records []model.Record `json:"records"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	if len(zd.Records) != 1 {
		t.Fatalf("expected exactly one synthesized record, got %+v", zd.Records)
	}
	r := zd.Records[0]
	if r.Name != "dns.example.com" || r.Type != model.TypeA || r.Default != "192.168.10.53" {
		t.Fatalf("unexpected synthesized record: %+v", r)
	}
	if len(r.VLANOverrides) != 1 || r.VLANOverrides[0].VLAN != "trusted" || r.VLANOverrides[0].Value != "192.168.10.53" {
		t.Fatalf("unexpected vlan overrides: %+v", r.VLANOverrides)
	}
}

func TestRenderDoesNotOverrideExplicitACMERecord(t *testing.T) {
	s := acmeEnabledSettings()
	rs := model.RecordSet{Records: []model.Record{
		{Name: "dns.example.com", Type: model.TypeA, Default: "10.0.0.9"},
	}}
	ips := selfip.Result{Primary: selfip.VLANAddrs{V4: "192.168.10.53"}}

	out, err := Render(s, rs, Options{ConfigDir: "/config", ACMESelfIPs: ips})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var zd struct {
		Records []model.Record `json:"records"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	if len(zd.Records) != 1 || zd.Records[0].Default != "10.0.0.9" {
		t.Fatalf("explicit user record should not be overridden/duplicated: %+v", zd.Records)
	}
}

func TestRenderSkipsACMESynthesisWhenNothingDetected(t *testing.T) {
	s := acmeEnabledSettings()
	s.Listeners.DoT.Enabled = false // no encrypted listener: DDR doesn't apply, so a missing address is fine

	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config"}) // zero ACMESelfIPs
	if err != nil {
		t.Fatalf("Render should not fail when nothing was detected: %v", err)
	}
	var zd struct {
		Records []model.Record `json:"records"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	if len(zd.Records) != 0 {
		t.Fatalf("expected no synthesized record, got %+v", zd.Records)
	}
}

func TestRenderACMESynthesisRespectsFamilySeparately(t *testing.T) {
	s := acmeEnabledSettings()
	ips := selfip.Result{Primary: selfip.VLANAddrs{V4: "192.168.10.53"}} // no V6

	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config", ACMESelfIPs: ips})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var zd struct {
		Records []model.Record `json:"records"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	for _, r := range zd.Records {
		if r.Type == model.TypeAAAA {
			t.Fatalf("no AAAA record should be synthesized without a detected V6 address: %+v", r)
		}
	}
}

func TestRenderZoneDataDDRRecords(t *testing.T) {
	s := acmeEnabledSettings()
	s.Listeners.DoH = model.Listener{Enabled: true, Port: 443}
	s.Listeners.DoQ = model.QUICListener{Listener: model.Listener{Enabled: true, Port: 8853}}
	rs := model.RecordSet{Records: []model.Record{
		{Name: "dns.example.com", Type: model.TypeA, Default: "192.168.10.53"},
	}}

	out, err := Render(s, rs, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var zd struct {
		DDR []ddrRecord `json:"ddr"`
	}
	if err := json.Unmarshal(out.ZoneData, &zd); err != nil {
		t.Fatalf("zone data is not valid JSON: %v\n%s", err, out.ZoneData)
	}
	if len(zd.DDR) != 3 {
		t.Fatalf("expected 3 DDR records (DoT/DoH/DoQ), got %+v", zd.DDR)
	}
	byAlpn := map[string]ddrRecord{}
	for _, d := range zd.DDR {
		if d.Target != "dns.example.com" {
			t.Fatalf("unexpected DDR target: %+v", d)
		}
		for _, p := range d.Params {
			if p.Key == "alpn" {
				byAlpn[p.Value] = d
			}
		}
	}
	dot, ok := byAlpn["dot"]
	if !ok {
		t.Fatalf("missing dot DDR record: %+v", zd.DDR)
	}
	if !hasParam(dot.Params, "port", "853") {
		t.Errorf("dot record missing port 853: %+v", dot.Params)
	}
	doh, ok := byAlpn["h2"]
	if !ok {
		t.Fatalf("missing h2 DDR record: %+v", zd.DDR)
	}
	if !hasParam(doh.Params, "port", "443") || !hasParam(doh.Params, "dohpath", "/dns-query{?dns}") {
		t.Errorf("h2 record missing port/dohpath: %+v", doh.Params)
	}
	doq, ok := byAlpn["doq"]
	if !ok {
		t.Fatalf("missing doq DDR record: %+v", zd.DDR)
	}
	if !hasParam(doq.Params, "port", "8853") {
		t.Errorf("doq record missing port 8853: %+v", doq.Params)
	}
}

func hasParam(params []ddrParam, key, value string) bool {
	for _, p := range params {
		if p.Key == key && p.Value == value {
			return true
		}
	}
	return false
}

func TestRenderNoDDRZoneWhenACMEDisabled(t *testing.T) {
	s := baseSettings() // ACME disabled; DoT enabled
	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	if strings.Contains(cf, "resolver.arpa") {
		t.Errorf("resolver.arpa should not be served without ACME configured:\n%s", cf)
	}
}

func TestRenderNoDDRZoneWhenNoEncryptedListenerEnabled(t *testing.T) {
	s := acmeEnabledSettings()
	s.Listeners.DoT.Enabled = false // only plain enabled now
	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	if strings.Contains(cf, "resolver.arpa") {
		t.Errorf("resolver.arpa should not be served with no encrypted listener enabled:\n%s", cf)
	}
}

// A DDR SVCB record whose target has no A/AAAA is worse than useless — it
// sends clients to an address that will NXDOMAIN, so opportunistic DoT/DoH/DoQ
// upgrade silently breaks. Render must refuse to produce that config instead
// of shipping it (regression: this used to succeed when selfip detection
// found nothing and no explicit record was declared for the ACME domain).
func TestRenderFailsWhenDDRTargetHasNoAddress(t *testing.T) {
	s := acmeEnabledSettings() // DoT enabled, no explicit record, zero ACMESelfIPs
	_, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config"})
	if err == nil {
		t.Fatal("expected an error: DDR is enabled but nothing resolves the target address")
	}
}

func TestRenderCorefileDoHAndDoQ(t *testing.T) {
	s := baseSettings()
	s.Listeners.DoH = model.Listener{Enabled: true, Port: 443}
	s.Listeners.DoQ = model.QUICListener{Listener: model.Listener{Enabled: true, Port: 853}}

	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	for _, want := range []string{
		"https://.:443 {",
		"quic://.:853 {",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Corefile missing %q:\n%s", want, cf)
		}
	}
	// Every encrypted block (dot/doh/doq — 3 of them here) needs its own tls line.
	if strings.Count(cf, "tls /c/cert.pem /c/key.pem") != 3 {
		t.Errorf("expected a tls line in each of the 3 encrypted blocks:\n%s", cf)
	}
	if strings.Contains(cf, "quic {") {
		t.Errorf("quic{} tuning block should be omitted when max_streams/worker_pool_size are unset:\n%s", cf)
	}
}

func TestRenderCorefileDoQTuning(t *testing.T) {
	s := baseSettings()
	s.Listeners.DoQ = model.QUICListener{
		Listener:       model.Listener{Enabled: true, Port: 8853},
		MaxStreams:     256,
		WorkerPoolSize: 512,
	}

	out, err := Render(s, model.RecordSet{}, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	for _, want := range []string{
		"quic://.:8853 {",
		"quic {",
		"max_streams 256",
		"worker_pool_size 512",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Corefile missing %q:\n%s", want, cf)
		}
	}
}

func TestValidateRejectsNegativeQUICTuning(t *testing.T) {
	s := baseSettings()
	s.Listeners.DoQ = model.QUICListener{Listener: model.Listener{Enabled: true, Port: 853}, MaxStreams: -1}
	if err := Validate(s, model.RecordSet{}); err == nil {
		t.Fatal("expected an error for negative max_streams")
	}
}

func TestRenderMDNSStanzaOnlyWhenEnabled(t *testing.T) {
	rs := model.RecordSet{}

	off := baseSettings() // mdns disabled by default
	out, err := Render(off, rs, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Corefile), "mdnsbridge") {
		t.Fatalf("mdnsbridge should not be rendered when disabled:\n%s", out.Corefile)
	}

	on := baseSettings()
	on.MDNS.Enabled = true
	out, err = Render(on, rs, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatal(err)
	}
	cf := string(out.Corefile)
	if strings.Count(cf, "mdnsbridge") != 2 { // one per server block (plain + dot)
		t.Fatalf("mdnsbridge should appear in both server blocks when enabled:\n%s", cf)
	}
	// It must be rendered ahead of localrecords within each block (readability;
	// actual chain order is fixed by dnsserver.Directives).
	if strings.Index(cf, "mdnsbridge") > strings.Index(cf, "localrecords") {
		t.Fatalf("mdnsbridge should be written before localrecords:\n%s", cf)
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

func TestRenderECSRewriteOnlyWhenEnabled(t *testing.T) {
	off := baseSettings() // ecs disabled by default, upstream already private
	out, err := Render(off, model.RecordSet{}, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(out.Corefile), "rewrite edns0 subnet") {
		t.Fatalf("rewrite edns0 subnet should not be rendered when ecs is disabled:\n%s", out.Corefile)
	}

	on := baseSettings()
	on.UpstreamDNS.ECS = true // baseSettings upstream (192.168.10.5:53) is private
	out, err = Render(on, model.RecordSet{}, Options{ConfigDir: "/config", CertFile: "/c/cert.pem", KeyFile: "/c/key.pem"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cf := string(out.Corefile)
	if strings.Count(cf, "rewrite edns0 subnet set 32 128") != 2 { // one per server block (plain + dot)
		t.Fatalf("expected rewrite edns0 subnet set 32 128 in both server blocks:\n%s", cf)
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
		"ecs enabled with public upstream": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.UpstreamDNS.Address = "1.1.1.1:53"
			s.UpstreamDNS.ECS = true
			return s, model.RecordSet{}
		},
		"ecs enabled with hostname upstream": func() (model.Settings, model.RecordSet) {
			s := baseSettings()
			s.UpstreamDNS.Address = "resolver.example.com:53"
			s.UpstreamDNS.ECS = true
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

func TestMDNSRecordExcludedFromZoneDataAndValidated(t *testing.T) {
	s := baseSettings()
	rs := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"},
		{Name: "printer.home.arpa", Type: model.TypeMDNS, Match: &model.Selector{Host: "printer.local"}},
	}}
	out, err := Render(s, rs, Options{ConfigDir: "/config"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	zd := string(out.ZoneData)
	if strings.Contains(zd, "printer.home.arpa") {
		t.Fatalf("mdns record must be excluded from localrecords zone data:\n%s", zd)
	}
	if !strings.Contains(zd, "nas.home.arpa") {
		t.Fatalf("static record should be in zone data:\n%s", zd)
	}

	bad := map[string]model.Record{
		"mdns without match": {Name: "x.home.arpa", Type: model.TypeMDNS},
		"mdns with default":  {Name: "x.home.arpa", Type: model.TypeMDNS, Default: "1.2.3.4", Match: &model.Selector{Host: "x.local"}},
		"match on non-mdns":  {Name: "x.home.arpa", Type: model.TypeA, Default: "1.2.3.4", Match: &model.Selector{Host: "x.local"}},
		"mdns bad vlan":      {Name: "x.home.arpa", Type: model.TypeMDNS, Match: &model.Selector{VLAN: "ghost"}},
	}
	for name, rec := range bad {
		t.Run(name, func(t *testing.T) {
			if err := Validate(baseSettings(), model.RecordSet{Records: []model.Record{rec}}); err == nil {
				t.Fatalf("expected validation error for %q", name)
			}
		})
	}
}

func TestValidateMDNSBlock(t *testing.T) {
	s := baseSettings()
	s.MDNS = model.MDNS{
		Enabled:     true,
		Suffix:      "not-in-zone.example.com", // outside owned zone → error
		AutoPublish: []model.Selector{{Service: "_airplay._tcp"}},
	}
	if err := Validate(s, model.RecordSet{}); err == nil {
		t.Fatal("suffix outside owned zone should be rejected")
	}

	s.MDNS.Suffix = "media.home.arpa"
	s.MDNS.AutoPublish = []model.Selector{{VLAN: "ghost"}} // unknown vlan
	if err := Validate(s, model.RecordSet{}); err == nil {
		t.Fatal("auto_publish selector with unknown vlan should be rejected")
	}

	s.MDNS.AutoPublish = []model.Selector{{Service: "_airplay._tcp", VLAN: "trusted"}}
	if err := Validate(s, model.RecordSet{}); err != nil {
		t.Fatalf("valid mdns block should pass: %v", err)
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
