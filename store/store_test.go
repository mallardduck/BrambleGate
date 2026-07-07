package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mallardduck/BrambleDNS/model"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	in := model.Settings{
		VLANs:       []model.VLAN{{Name: "trusted", CIDR: "192.168.10.0/24"}},
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
	if got.UpstreamDNS != in.UpstreamDNS || len(got.VLANs) != 1 || got.VLANs[0].CIDR != "192.168.10.0/24" {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}

func TestRecordsRoundTripPreservesNullOverride(t *testing.T) {
	s := New(t.TempDir())
	deny := (*string)(nil) // explicit NXDOMAIN for a VLAN
	trusted := "192.168.10.20"
	in := model.RecordSet{Records: []model.Record{
		{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20", VLANOverrides: []model.VLANOverride{
			{VLAN: "untrusted-wifi", Value: deny},
			{VLAN: "trusted", Value: &trusted},
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
	nas := got.Records[0]
	if len(nas.VLANOverrides) != 2 || nas.VLANOverrides[0].Value != nil {
		t.Fatalf("null override not preserved: %+v", nas.VLANOverrides)
	}
	if nas.VLANOverrides[1].Value == nil || *nas.VLANOverrides[1].Value != "192.168.10.20" {
		t.Fatalf("non-null override not preserved: %+v", nas.VLANOverrides[1])
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
