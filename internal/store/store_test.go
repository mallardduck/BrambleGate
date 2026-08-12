package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mallardduck/BrambleGate/model"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	in := model.Settings{
		VLANs:       []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24", "fd00:10::/64"}}},
		UpstreamDNS: model.UpstreamTarget{Address: "192.168.10.5:53", Protocol: "plain"},
		Listeners:   model.Listeners{Plain: model.Listener{Enabled: true, Port: 53}},
		ACME:        model.ACME{Enabled: true, Domain: "dns.example.com"},
	}
	if err := s.SaveSettings(in); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.UpstreamDNS != in.UpstreamDNS || len(got.VLANs) != 1 || len(got.VLANs[0].CIDRs) != 2 || got.VLANs[0].CIDRs[1] != "fd00:10::/64" {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}

func TestSettingsExistAndSeedDefaults(t *testing.T) {
	s := New(t.TempDir())
	if s.SettingsExist() {
		t.Fatal("SettingsExist should be false before any settings.yaml is written")
	}
	if err := s.SaveSettings(model.DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings(defaults): %v", err)
	}
	if !s.SettingsExist() {
		t.Fatal("SettingsExist should be true after seeding defaults")
	}
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.UpstreamDNS.Address == "" || !got.Listeners.Plain.Enabled {
		t.Fatalf("seeded defaults not usable: %+v", got)
	}
}

func TestRecordsRoundTripPreservesOverrides(t *testing.T) {
	s := New(t.TempDir())
	in := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20", TTL: 300, VLANOverrides: []model.VLANOverride{
			{VLAN: "untrusted-wifi", NXDomain: true},
			{VLAN: "smarthome", Value: "192.168.20.5", TTL: 60},
			{VLAN: "guests", TTL: 30}, // TTL-only: inherits the default value
		}},
		{Name: "git.home.arpa", Type: model.TypeCNAME, Default: "nas.home.arpa"},
	}}
	if err := s.SaveRecords(in); err != nil {
		t.Fatalf("SaveRecords: %v", err)
	}
	got, err := s.LoadRecords()
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("want 2 records, got %d", len(got.Records))
	}
	o := got.Records[0].VLANOverrides
	if len(o) != 3 {
		t.Fatalf("want 3 overrides, got %d", len(o))
	}
	if !o[0].NXDomain {
		t.Fatalf("nxdomain override not preserved: %+v", o[0])
	}
	if o[1].Value != "192.168.20.5" || o[1].TTL != 60 {
		t.Fatalf("value+ttl override not preserved: %+v", o[1])
	}
	if o[2].Value != "" || o[2].TTL != 30 {
		t.Fatalf("ttl-only override not preserved: %+v", o[2])
	}
}

func TestHostsRoundTripPreservesAliases(t *testing.T) {
	s := New(t.TempDir())
	in := model.HostSet{Hosts: []model.Host{
		{IP: "192.168.10.55", Hostname: "kitchen-tablet.home.arpa", Aliases: []string{"tablet.home.arpa"}},
		{IP: "192.168.10.20", Hostname: "nas.home.arpa"},
	}}
	if err := s.SaveHosts(in); err != nil {
		t.Fatalf("SaveHosts: %v", err)
	}
	got, err := s.LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts: %v", err)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("want 2 hosts, got %d", len(got.Hosts))
	}
	if len(got.Hosts[0].Aliases) != 1 || got.Hosts[0].Aliases[0] != "tablet.home.arpa" {
		t.Fatalf("aliases not preserved: %+v", got.Hosts[0])
	}
	if len(got.Hosts[1].Aliases) != 0 {
		t.Fatalf("want no aliases on second host, got %+v", got.Hosts[1])
	}
}

func TestLoadHostsMissingIsEmpty(t *testing.T) {
	got, err := New(t.TempDir()).LoadHosts()
	if err != nil {
		t.Fatalf("LoadHosts on missing file should be nil error, got %v", err)
	}
	if len(got.Hosts) != 0 {
		t.Fatalf("want empty host set, got %+v", got)
	}
}

func TestLoadRecordsMissingIsEmpty(t *testing.T) {
	got, err := New(t.TempDir()).LoadRecords()
	if err != nil {
		t.Fatalf("LoadRecords on missing file should be nil error, got %v", err)
	}
	if len(got.Records) != 0 {
		t.Fatalf("want empty record set, got %+v", got)
	}
}

func TestAtomicWriteNoTempLeftBehind(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SaveSettings(model.Settings{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' && e.Name() != ".runtime" {
			// tmp files are ".settings.yaml.tmp-*"; none should survive.
			if e.Name() != settingsFile {
				t.Fatalf("stray temp file left behind: %s", e.Name())
			}
		}
	}
}
