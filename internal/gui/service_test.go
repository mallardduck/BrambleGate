package gui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/configgen"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/store"
)

// stubReloader records the last Corefile and can be made to fail.
type stubReloader struct {
	calls    int
	last     []byte
	failWith error
}

func (s *stubReloader) Reload(corefile []byte) error {
	s.calls++
	s.last = corefile
	return s.failWith
}

func newService(t *testing.T) (*Service, *store.Store, *stubReloader) {
	t.Helper()
	dir := t.TempDir()
	st := store.New(dir)
	if err := st.SaveSettings(model.Settings{
		UpstreamDNS: model.UpstreamTarget{Address: "192.168.10.5:53", Protocol: "plain"},
		Listeners:   model.Listeners{Plain: model.Listener{Enabled: true, Port: 53}},
	}); err != nil {
		t.Fatal(err)
	}
	rl := &stubReloader{}
	return NewService(st, rl, dir, configgen.Options{}), st, rl
}

func TestMDNSDisabledWhenNoTable(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.MDNSCandidates(); !IsValidation(err) {
		t.Fatalf("expected ErrMDNSDisabled (validation), got %v", err)
	}
	if err := svc.PromoteMDNS("x.home.arpa"); !IsValidation(err) {
		t.Fatalf("promote with mDNS off should be a validation error, got %v", err)
	}
}

func TestMDNSPublishAndPromote(t *testing.T) {
	svc, st, rl := newService(t)
	tbl := mdnsbridge.NewTable(mdnsbridge.Config{DefaultSuffix: "home.arpa"}, time.Minute) // no auto-publish
	tbl.Upsert(mdnsbridge.Entry{Host: "printer.local.", Service: "_ipp._tcp", Instance: "Office", IPv4: []string{"192.168.1.9"}})
	svc.SetMDNSTable(tbl)

	// Visible as a candidate, not yet published (mapped into home.arpa).
	cands, err := svc.MDNSCandidates()
	if err != nil || len(cands) != 1 || cands[0].Published || cands[0].Name != "printer.home.arpa." {
		t.Fatalf("expected 1 unpublished candidate mapped to printer.home.arpa., got %+v (err %v)", cands, err)
	}

	// Approve (publish) — runtime only, no reload, no record written.
	if err := svc.SetMDNSPublished("printer.home.arpa", true); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if rl.calls != 0 {
		t.Fatalf("publish should not reload the engine, calls=%d", rl.calls)
	}

	// Promote — writes a live type:mdns record (reload) linked by host selector.
	if err := svc.PromoteMDNS("printer.home.arpa"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	rs, _ := st.LoadRecords()
	if len(rs.Records) != 1 || !rs.Records[0].IsMDNS() || rs.Records[0].Name != "printer.home.arpa" {
		t.Fatalf("promote should write a type:mdns record, got %+v", rs.Records)
	}
	if rs.Records[0].Match == nil || rs.Records[0].Match.Host != "printer.local" {
		t.Fatalf("promoted record should link by host selector, got %+v", rs.Records[0].Match)
	}
	if rl.calls != 1 {
		t.Fatalf("promote should reload once, calls=%d", rl.calls)
	}
	// The promoted binding now resolves live from the table (not a frozen copy).
	v4, _, owned := tbl.Resolve("printer.home.arpa")
	if !owned || len(v4) != 1 || v4[0] != "192.168.1.9" {
		t.Fatalf("promoted name should resolve live, got owned=%v v4=%v", owned, v4)
	}
}

func TestPromoteUnknownIsNotFound(t *testing.T) {
	svc, _, _ := newService(t)
	svc.SetMDNSTable(mdnsbridge.NewTable(mdnsbridge.Config{DefaultSuffix: "home.arpa"}, time.Minute))
	if err := svc.PromoteMDNS("ghost.home.arpa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAddRecordRendersAndReloads(t *testing.T) {
	svc, st, rl := newService(t)
	err := svc.AddRecord(model.Record{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"})
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}
	if rl.calls != 1 {
		t.Fatalf("reload calls = %d, want 1", rl.calls)
	}
	if !strings.Contains(string(rl.last), "zonedata") {
		t.Fatalf("reloaded Corefile should point localrecords at zonedata:\n%s", rl.last)
	}
	// The record itself lives in the JSON zone data written before reload.
	zone, err := os.ReadFile(configgen.ZoneDataPath(st.Dir()))
	if err != nil {
		t.Fatalf("zone data not written: %v", err)
	}
	if !strings.Contains(string(zone), "192.168.10.20") || !strings.Contains(string(zone), "nas.home.arpa") {
		t.Fatalf("zone data missing the record:\n%s", zone)
	}
	rs, _ := st.LoadRecords()
	if len(rs.Records) != 1 {
		t.Fatalf("records.yaml should have 1 record, got %d", len(rs.Records))
	}
}

func TestAddInvalidRecordDoesNotWriteOrReload(t *testing.T) {
	svc, st, rl := newService(t)
	err := svc.AddRecord(model.Record{Name: "nas.home.arpa", Type: model.TypeA, Default: "not-an-ip"})
	if err == nil || !IsValidation(err) {
		t.Fatalf("want validation error, got %v", err)
	}
	if rl.calls != 0 {
		t.Fatalf("invalid record must not trigger reload, calls = %d", rl.calls)
	}
	rs, _ := st.LoadRecords()
	if len(rs.Records) != 0 {
		t.Fatalf("invalid record must not be persisted, got %d records", len(rs.Records))
	}
}

func TestAddDuplicateRejected(t *testing.T) {
	svc, _, _ := newService(t)
	r := model.Record{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"}
	if err := svc.AddRecord(r); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddRecord(r); err == nil || !IsValidation(err) {
		t.Fatalf("duplicate should be a validation error, got %v", err)
	}
}

func TestDeleteMissingIsNotFound(t *testing.T) {
	svc, _, _ := newService(t)
	if err := svc.DeleteRecord("ghost.home.arpa", model.TypeA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReloadFailurePersistsButSurfaces(t *testing.T) {
	svc, st, rl := newService(t)
	rl.failWith = errors.New("port 53 in use")
	err := svc.AddRecord(model.Record{Name: "x.home.arpa", Type: model.TypeA, Default: "10.0.0.1"})
	if err == nil {
		t.Fatal("expected reload failure to surface")
	}
	if IsValidation(err) {
		t.Fatalf("reload failure must not be classified as validation: %v", err)
	}
	// The record WAS persisted (saved) even though it could not be applied.
	rs, _ := st.LoadRecords()
	if len(rs.Records) != 1 {
		t.Fatalf("record should be persisted despite reload failure, got %d", len(rs.Records))
	}
}
