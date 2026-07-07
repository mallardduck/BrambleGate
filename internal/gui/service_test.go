package gui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleDNS/configgen"
	"github.com/mallardduck/BrambleDNS/model"
	"github.com/mallardduck/BrambleDNS/store"
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

func TestAddRecordRendersAndReloads(t *testing.T) {
	svc, st, rl := newService(t)
	err := svc.AddRecord(model.Record{Name: "nas.home.arpa", Type: model.TypeA, Default: "192.168.10.20"})
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}
	if rl.calls != 1 {
		t.Fatalf("reload calls = %d, want 1", rl.calls)
	}
	if !strings.Contains(string(rl.last), "record nas.home.arpa A 192.168.10.20") {
		t.Fatalf("reloaded Corefile missing the record:\n%s", rl.last)
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
