package gui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/internal/configgen"
	"github.com/mallardduck/BrambleGate/internal/configgen/selfip"
	"github.com/mallardduck/BrambleGate/internal/gatewaydetect"
	"github.com/mallardduck/BrambleGate/internal/store"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
	"github.com/mallardduck/BrambleGate/plugins/querylog"
)

// stubSelfIPs replaces detectSelfIPs for the duration of a test, then restores
// it — the same save/restore pattern used for newMDNSAdvertiser/runMDNSListener
// elsewhere in this package.
func stubSelfIPs(t *testing.T, res selfip.Result) {
	t.Helper()
	orig := detectSelfIPs
	detectSelfIPs = func(vlans []model.VLAN) selfip.Result { return res }
	t.Cleanup(func() { detectSelfIPs = orig })
}

// stubVLANCandidates replaces detectVLANCandidates for the duration of a test.
func stubVLANCandidates(t *testing.T, cands []selfip.Candidate) {
	t.Helper()
	orig := detectVLANCandidates
	detectVLANCandidates = func(existing []model.VLAN) []selfip.Candidate { return cands }
	t.Cleanup(func() { detectVLANCandidates = orig })
}

// stubGateways replaces detectGateways for the duration of a test — same
// save/restore pattern as stubSelfIPs.
func stubGateways(t *testing.T, res gatewaydetect.Result) {
	t.Helper()
	orig := detectGateways
	detectGateways = func(vlans []model.VLAN) gatewaydetect.Result { return res }
	t.Cleanup(func() { detectGateways = orig })
}

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
	log := slog.New(slog.DiscardHandler)
	return NewService(t.Context(), st, rl, dir, configgen.Options{}, log), st, rl
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

	// Deleting a promoted record round-trips its type through a URL path
	// (handlers_ui.go/server.go uppercase the {type} param for a
	// case-insensitive API), so the lookup must match "MDNS" against the
	// stored lowercase model.TypeMDNS, not reject it as not-found.
	if err := svc.DeleteRecord("printer.home.arpa", model.RecordType(strings.ToUpper(string(model.TypeMDNS)))); err != nil {
		t.Fatalf("delete promoted record: %v", err)
	}
	rs, _ = st.LoadRecords()
	if len(rs.Records) != 0 {
		t.Fatalf("expected promoted record to be deleted, got %+v", rs.Records)
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

// mdnsListenerCall records one runMDNSListener invocation and lets a test wait
// for its ctx to be canceled (i.e. the "goroutine" to have stopped).
type mdnsListenerCall struct {
	services, ifaces []string
	stopped          chan struct{}
}

// stubMDNSListener replaces runMDNSListener for the duration of the test,
// publishing each invocation on the returned channel instead of touching real
// mDNS multicast sockets. Because StartMDNS launches it with `go`, tests must
// receive from this channel (waitForMDNSCall) rather than poll a slice — there
// is no other signal that the goroutine has actually run yet.
func stubMDNSListener(t *testing.T) chan *mdnsListenerCall {
	t.Helper()
	calls := make(chan *mdnsListenerCall, 8)

	orig := runMDNSListener
	runMDNSListener = func(ctx context.Context, _ *mdnsbridge.Table, services, ifaces []string, _ *slog.Logger) {
		call := &mdnsListenerCall{services: services, ifaces: ifaces, stopped: make(chan struct{})}
		calls <- call
		<-ctx.Done()
		close(call.stopped)
	}
	t.Cleanup(func() { runMDNSListener = orig })

	return calls
}

func waitForMDNSCall(t *testing.T, calls chan *mdnsListenerCall) *mdnsListenerCall {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the listener goroutine to start")
		return nil
	}
}

func TestSaveSettingsEnablesMDNSListenerLive(t *testing.T) {
	svc, _, _ := newService(t)
	calls := stubMDNSListener(t)

	if _, err := svc.MDNSCandidates(); !IsValidation(err) {
		t.Fatalf("expected mDNS disabled before enabling, got %v", err)
	}

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS = model.MDNS{Enabled: true, ServiceTypes: []string{"_http._tcp"}, Interfaces: []string{"all"}}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitForMDNSCall(t, calls)

	if _, err := svc.MDNSCandidates(); err != nil {
		t.Fatalf("expected mDNS enabled live (no restart), got %v", err)
	}
}

func TestSaveSettingsRestartsMDNSListenerOnReconfigure(t *testing.T) {
	svc, _, _ := newService(t)
	calls := stubMDNSListener(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS = model.MDNS{Enabled: true, ServiceTypes: []string{"_http._tcp"}, Interfaces: []string{"all"}}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}
	first := waitForMDNSCall(t, calls)

	settings.MDNS.ServiceTypes = []string{"_ipp._tcp"}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	second := waitForMDNSCall(t, calls)

	select {
	case <-first.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the original listener goroutine to be stopped on reconfigure")
	}
	if !slices.Equal(second.services, []string{"_ipp._tcp"}) {
		t.Fatalf("expected restarted listener to use the new service types, got %v", second.services)
	}
}

// A service-types/interfaces reconfigure must flush the table: entries
// discovered under the old filter aren't refreshed by the new browse
// config, so they should disappear immediately from /mdns candidates
// rather than lingering until their TTL naturally expires.
func TestSaveSettingsReconfigureFlushesStaleTableEntries(t *testing.T) {
	svc, _, _ := newService(t)
	calls := stubMDNSListener(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS = model.MDNS{Enabled: true, ServiceTypes: []string{"_http._tcp"}, Interfaces: []string{"all"}}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitForMDNSCall(t, calls)

	svc.mdns.Upsert(mdnsbridge.Entry{Host: "printer.local.", Service: "_http._tcp", Instance: "Printer", IPv4: []string{"192.168.1.9"}})
	if got, err := svc.MDNSCandidates(); err != nil || len(got) != 1 {
		t.Fatalf("setup: expected 1 candidate before reconfigure, got %v err=%v", got, err)
	}

	settings.MDNS.ServiceTypes = []string{"_ipp._tcp"}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	waitForMDNSCall(t, calls)

	got, err := svc.MDNSCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("candidates after reconfigure = %v, want none — stale entry should have been flushed", got)
	}
}

func TestSaveSettingsDisablesMDNSListenerLive(t *testing.T) {
	svc, _, _ := newService(t)
	calls := stubMDNSListener(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS = model.MDNS{Enabled: true, ServiceTypes: []string{"_http._tcp"}, Interfaces: []string{"all"}}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}
	running := waitForMDNSCall(t, calls)

	settings.MDNS.Enabled = false
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("disable: %v", err)
	}

	select {
	case <-running.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the listener goroutine to be stopped on disable")
	}
	if _, err := svc.MDNSCandidates(); !IsValidation(err) {
		t.Fatalf("expected mDNS disabled live (no restart), got %v", err)
	}
}

// stubAdvertiser is a bare mdnsAdvertiser fake — real Reconcile/Close semantics
// (registering/unregistering mDNS-SD services) are covered by
// plugins/mdnsadvertise's own tests; here we only need to observe that
// Service calls the interface correctly.
type stubAdvertiser struct {
	reconcileCalls int
	lastSettings   model.Settings
	closed         bool
}

func (a *stubAdvertiser) Reconcile(settings model.Settings) {
	a.reconcileCalls++
	a.lastSettings = settings
}

func (a *stubAdvertiser) Close() error {
	a.closed = true
	return nil
}

func stubMDNSAdvertiser(t *testing.T) *[]*stubAdvertiser {
	t.Helper()
	var created []*stubAdvertiser

	orig := newMDNSAdvertiser
	newMDNSAdvertiser = func(context.Context, *slog.Logger) (mdnsAdvertiser, error) {
		a := &stubAdvertiser{}
		created = append(created, a)
		return a, nil
	}
	t.Cleanup(func() { newMDNSAdvertiser = orig })

	return &created
}

func TestSaveSettingsStartsAdvertiseLive(t *testing.T) {
	svc, _, _ := newService(t)
	created := stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS.Advertise.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if len(*created) != 1 {
		t.Fatalf("expected exactly one advertiser created, got %d", len(*created))
	}
	if (*created)[0].reconcileCalls != 1 {
		t.Fatalf("expected Reconcile called once, got %d", (*created)[0].reconcileCalls)
	}
}

func TestSaveSettingsReconcilesAdvertiseWithoutRecreating(t *testing.T) {
	svc, _, _ := newService(t)
	created := stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS.Advertise.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}

	settings.Listeners.DoT = model.Listener{Enabled: true, Port: 853}
	settings.ACME.Domain = "dns.example.com"
	settings.ACME.SelfSignedFallback = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable dot: %v", err)
	}

	if len(*created) != 1 {
		t.Fatalf("expected the advertiser to be reused, not recreated; got %d instances", len(*created))
	}
	if (*created)[0].reconcileCalls != 2 {
		t.Fatalf("expected Reconcile called on every save, got %d", (*created)[0].reconcileCalls)
	}
}

func TestSaveSettingsStopsAdvertiseLive(t *testing.T) {
	svc, _, _ := newService(t)
	created := stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS.Advertise.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}

	settings.MDNS.Advertise.Enabled = false
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if !(*created)[0].closed {
		t.Fatal("expected the advertiser to be closed on disable")
	}
}

func TestAdvertiseIsIndependentOfMDNSEnabled(t *testing.T) {
	svc, _, _ := newService(t)
	created := stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.MDNS.Enabled = false
	settings.MDNS.Advertise.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable advertise only: %v", err)
	}

	if len(*created) != 1 {
		t.Fatalf("expected advertising to start even with discovery disabled, got %d instances", len(*created))
	}
	if _, err := svc.MDNSCandidates(); !IsValidation(err) {
		t.Fatalf("expected discovery to remain disabled, got %v", err)
	}
}

func TestACMESelfRecords(t *testing.T) {
	svc, st, _ := newService(t)
	stubSelfIPs(t, selfip.Result{
		PerVLAN: map[string]selfip.VLANAddrs{"trusted": {V4: "192.168.10.53"}},
		Primary: selfip.VLANAddrs{V4: "192.168.10.53"},
	})

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.VLANs = []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}
	settings.ACME = model.ACME{Enabled: true, Domain: "dns.example.com", Email: "a@example.com", DNSProvider: "cloudflare"}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	recs, err := svc.ACMESelfRecords()
	if err != nil {
		t.Fatalf("ACMESelfRecords: %v", err)
	}
	if len(recs) != 1 || recs[0].Default != "192.168.10.53" {
		t.Fatalf("unexpected self records: %+v", recs)
	}

	// Never persisted to records.yaml.
	rs, err := st.LoadRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Records) != 0 {
		t.Fatalf("ACMESelfRecords must not write records.yaml, got %+v", rs.Records)
	}
}

func TestACMESelfRecordsReflectsFreshDetectionOnEachSave(t *testing.T) {
	svc, _, _ := newService(t)
	stubSelfIPs(t, selfip.Result{Primary: selfip.VLANAddrs{V4: "192.168.10.53"}})

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ACME = model.ACME{Enabled: true, Domain: "dns.example.com", Email: "a@example.com", DNSProvider: "cloudflare"}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	first, err := svc.ACMESelfRecords()
	if err != nil || len(first) != 1 || first[0].Default != "192.168.10.53" {
		t.Fatalf("unexpected first detection: %+v (err %v)", first, err)
	}

	stubSelfIPs(t, selfip.Result{Primary: selfip.VLANAddrs{V4: "192.168.10.99"}})
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings (2nd): %v", err)
	}
	second, err := svc.ACMESelfRecords()
	if err != nil || len(second) != 1 || second[0].Default != "192.168.10.99" {
		t.Fatalf("expected fresh detection on 2nd save, got: %+v (err %v)", second, err)
	}
}

func TestClientsDisabledWhenNotEnabled(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Clients(); !errors.Is(err, ErrClientNamesDisabled) {
		t.Fatalf("expected ErrClientNamesDisabled, got %v", err)
	}
}

func TestSaveSettingsStartsAndStopsClientNamesLive(t *testing.T) {
	svc, _, _ := newService(t)
	t.Cleanup(func() { querylog.SetClientObserver(nil) })

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ClientNames.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if entries, err := svc.Clients(); err != nil {
		t.Fatalf("Clients() after enable: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("expected an empty client cache right after enabling, got %+v", entries)
	}

	settings.ClientNames.Enabled = false
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := svc.Clients(); !errors.Is(err, ErrClientNamesDisabled) {
		t.Fatalf("expected ErrClientNamesDisabled after disable, got %v", err)
	}
}

func TestAddHostRefreshesClientNamesHostsIndex(t *testing.T) {
	svc, _, _ := newService(t)
	t.Cleanup(func() { querylog.SetClientObserver(nil) })

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ClientNames.Enabled = true
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if _, err := svc.AddHost(model.Host{IP: "192.168.10.47", Hostname: "nas.home.arpa"}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	name, source := svc.clientNames.Resolve("192.168.10.47")
	if name != "nas.home.arpa" || source != "hosts" {
		t.Fatalf("got %q/%q, want nas.home.arpa/hosts after AddHost refreshed the tier-0 index", name, source)
	}
}

func TestClientNamesConfigDefaultsToAutoDetectedGateways(t *testing.T) {
	svc, _, _ := newService(t)
	stubGateways(t, gatewaydetect.Result{
		PerVLAN: map[string]string{"trusted": "192.168.10.1"},
		Primary: "192.168.10.1",
	})

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.VLANs = []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}

	cfg := svc.clientNamesConfig(settings, model.HostSet{})
	if _, ok := cfg.Resolvers["trusted"]; !ok {
		t.Fatalf("expected an auto-detected PTR resolver for VLAN trusted (no ptr_upstream set), got %+v", cfg.Resolvers)
	}
	if cfg.UnmatchedResolver == nil {
		t.Fatal("expected UnmatchedResolver to be populated from gatewaydetect.Result.Primary")
	}
}

func TestClientNamesConfigExplicitOverrideWinsOverAutoDetect(t *testing.T) {
	svc, _, _ := newService(t)
	stubGateways(t, gatewaydetect.Result{PerVLAN: map[string]string{"trusted": "192.168.10.1"}})

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.VLANs = []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}
	settings.ClientNames.PTRUpstream = "10.0.0.53:53"

	cfg := svc.clientNamesConfig(settings, model.HostSet{})
	if _, ok := cfg.Resolvers["trusted"]; !ok {
		t.Fatal("expected the explicit ptr_upstream override applied to VLAN trusted")
	}
	if cfg.UnmatchedResolver == nil {
		t.Fatal("expected the explicit ptr_upstream override to also populate UnmatchedResolver")
	}
}

func TestVLANCandidates(t *testing.T) {
	svc, _, _ := newService(t)
	want := []selfip.Candidate{{CIDR: "192.168.30.0/23", SampleIP: "192.168.31.164", Suggested: "net-192-168-30-0"}}
	stubVLANCandidates(t, want)

	got, err := svc.VLANCandidates()
	if err != nil {
		t.Fatalf("VLANCandidates: %v", err)
	}
	if len(got) != 1 || got[0].CIDR != want[0].CIDR {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}
