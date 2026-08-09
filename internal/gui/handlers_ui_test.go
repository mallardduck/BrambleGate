package gui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleGate/model"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	svc, _, _ := newService(t)
	return NewServer(svc, ":0").Handler
}

func hxRequest(t *testing.T, method, target string, form url.Values) *http.Request {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	req.Header.Set("HX-Request", "true")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func TestDashboardPageRenders(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "BrambleGate") {
		t.Fatalf("expected layout chrome in full-page response, got: %s", rec.Body.String())
	}
	// newService seeds a real upstream (192.168.10.5:53), not the onboarding
	// default, so the banner should NOT show.
	if strings.Contains(rec.Body.String(), "Point BrambleGate at your") {
		t.Fatalf("did not expect onboarding banner once a real upstream is set, got: %s", rec.Body.String())
	}
}

func TestDashboardShowsOnboardingBannerForDefaultUpstream(t *testing.T) {
	svc, st, _ := newService(t)
	settings, err := st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.UpstreamDNS.Address = "1.1.1.1:53"
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "Point BrambleGate at your") {
		t.Fatalf("expected onboarding banner for default upstream, got: %s", rec.Body.String())
	}
}

func TestRecordsCreateEditDelete(t *testing.T) {
	h := newTestServer(t)

	// Create.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/records", url.Values{
		"name": {"nas.home.arpa"}, "type": {"A"}, "default": {"192.168.10.20"}, "ttl": {"300"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nas.home.arpa") {
		t.Fatalf("expected new record in table, got: %s", rec.Body.String())
	}

	// Edit form pre-fill.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodGet, "/records/nas.home.arpa/A/edit", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "value=\"nas.home.arpa\"") {
		t.Fatalf("edit prefill status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Update.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPut, "/records/nas.home.arpa/A", url.Values{
		"name": {"nas.home.arpa"}, "type": {"A"}, "default": {"192.168.10.99"}, "ttl": {"300"},
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "192.168.10.99") {
		t.Fatalf("update status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Delete.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodDelete, "/records/nas.home.arpa/A", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No records yet.") {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRecordsCreateDuplicateShowsFormError(t *testing.T) {
	h := newTestServer(t)
	form := url.Values{"name": {"dup.home.arpa"}, "type": {"A"}, "default": {"192.168.10.1"}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/records", form))
	if rec.Code != http.StatusOK {
		t.Fatalf("first create status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/records", form))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("expected duplicate FormError, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// A promoted mDNS record's value comes from the live discovery table (Match),
// not Default/TTL/overrides — the generic add/edit form has no fields for
// that, so editing one there previously rendered a broken-looking form (type
// selected nothing, default looked emptily "valid"). It should be steered to
// the mDNS page instead of edited here.
func TestRecordsEditRejectsMDNSRecord(t *testing.T) {
	svc, st, _ := newService(t)
	h := NewServer(svc, ":0").Handler

	if err := st.SaveRecords(model.RecordSet{Records: []model.Record{
		{Name: "printer.home.arpa", Type: model.TypeMDNS, Match: &model.Selector{Host: "printer"}},
	}}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodGet, "/records/printer.home.arpa/mdns/edit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Edit mdns printer.home.arpa") {
		t.Fatalf("did not expect the edit form to render for an mDNS record, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "managed from the mDNS page") {
		t.Fatalf("expected steer-to-mDNS-page message, got: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPut, "/records/printer.home.arpa/mdns", url.Values{
		"name": {"printer.home.arpa"}, "type": {"A"}, "default": {"192.168.10.50"},
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed from the mDNS page") {
		t.Fatalf("expected update to be rejected, got status=%d body=%s", rec.Code, rec.Body.String())
	}

	rs, err := st.LoadRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Records) != 1 || rs.Records[0].Type != model.TypeMDNS {
		t.Fatalf("expected the mDNS record to be untouched, got: %+v", rs.Records)
	}
}

func TestSettingsSaveAndVLANLifecycle(t *testing.T) {
	h := newTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", url.Values{
		"upstream_address": {"10.0.0.5:53"}, "upstream_protocol": {"plain"},
		"plain_enabled": {"on"}, "plain_port": {"53"},
		"acme_renew_before_days": {"30"},
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `value="10.0.0.5:53"`) {
		t.Fatalf("settings save status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings/vlans", url.Values{
		"vlan_name": {"guest"}, "vlan_cidrs": {"192.168.30.0/24"},
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "guest") {
		t.Fatalf("vlan add status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodDelete, "/settings/vlans/guest", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "guest") {
		t.Fatalf("vlan remove status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsSave_ECS(t *testing.T) {
	svc, st, _ := newService(t)
	h := NewServer(svc, ":0").Handler

	// A private upstream with ECS enabled must save successfully and persist.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", url.Values{
		"upstream_address": {"192.168.10.5:53"}, "upstream_protocol": {"plain"},
		"upstream_ecs_enabled": {"on"},
		"plain_enabled":        {"on"}, "plain_port": {"53"},
		"acme_renew_before_days": {"30"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("save (private upstream + ecs) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err := st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.UpstreamDNS.ECS {
		t.Fatalf("expected ecs_enabled to persist as true, got: %+v", settings.UpstreamDNS)
	}

	// A public upstream with ECS enabled must be rejected, and the prior (valid)
	// settings must remain on disk unchanged.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", url.Values{
		"upstream_address": {"1.1.1.1:53"}, "upstream_protocol": {"plain"},
		"upstream_ecs_enabled": {"on"},
		"plain_enabled":        {"on"}, "plain_port": {"53"},
		"acme_renew_before_days": {"30"},
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ecs_enabled") {
		t.Fatalf("expected the public-upstream+ecs save to be rejected with an ecs_enabled error, status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err = st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.UpstreamDNS.Address != "192.168.10.5:53" {
		t.Fatalf("rejected save must not have overwritten settings.yaml, got: %+v", settings.UpstreamDNS)
	}
}

// max_fails is a *uint32 specifically so an explicit 0 (disable down-marking)
// is distinguishable from "unset, use CoreDNS's default" — this exercises
// both states plus blank clearing an already-set value back to unset.
func TestSettingsSave_ForwardTuning(t *testing.T) {
	svc, st, _ := newService(t)
	h := NewServer(svc, ":0").Handler

	base := func(extra url.Values) url.Values {
		form := url.Values{
			"upstream_address": {"192.168.10.5:53"}, "upstream_protocol": {"plain"},
			"plain_enabled": {"on"}, "plain_port": {"53"},
			"acme_renew_before_days": {"30"},
		}
		for k, v := range extra {
			form[k] = v
		}
		return form
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", base(url.Values{
		"upstream_max_fails":            {"0"},
		"upstream_health_check_seconds": {"30"},
		"upstream_expire_seconds":       {"20"},
		"upstream_max_concurrent":       {"500"},
		"upstream_prefer_udp":           {"on"},
	})))
	if rec.Code != http.StatusOK {
		t.Fatalf("save (forward tuning) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err := st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	u := settings.UpstreamDNS
	if u.MaxFails == nil || *u.MaxFails != 0 {
		t.Fatalf("expected max_fails=0 (explicit, not unset), got: %+v", u.MaxFails)
	}
	if u.HealthCheckSeconds != 30 || u.ExpireSeconds != 20 || u.MaxConcurrent != 500 || !u.PreferUDP {
		t.Fatalf("unexpected forward tuning persisted: %+v", u)
	}

	// Blank fields clear previously-set tuning back to unset/default.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", base(nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save (clear tuning) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err = st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	u = settings.UpstreamDNS
	if u.MaxFails != nil || u.HealthCheckSeconds != 0 || u.ExpireSeconds != 0 || u.MaxConcurrent != 0 || u.PreferUDP {
		t.Fatalf("expected all forward tuning cleared, got: %+v", u)
	}
}

func TestSettingsSave_CacheLogErrorsTuning(t *testing.T) {
	svc, st, _ := newService(t)
	h := NewServer(svc, ":0").Handler

	base := func(extra url.Values) url.Values {
		form := url.Values{
			"upstream_address": {"192.168.10.5:53"}, "upstream_protocol": {"plain"},
			"plain_enabled": {"on"}, "plain_port": {"53"},
			"acme_renew_before_days": {"30"},
		}
		for k, v := range extra {
			form[k] = v
		}
		return form
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", base(url.Values{
		"cache_serve_stale_disabled":  {"on"},
		"cache_prefetch_disabled":     {"on"},
		"log_disabled":                {"on"},
		"log_classes":                 {"denial, error"},
		"errors_consolidate_disabled": {"on"},
		"bufsize_disabled":            {"on"},
	})))
	if rec.Code != http.StatusOK {
		t.Fatalf("save (cache/log/errors/bufsize tuning) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err := st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Cache.ServeStaleDisabled || !settings.Cache.PrefetchDisabled {
		t.Fatalf("expected cache knobs disabled, got: %+v", settings.Cache)
	}
	if !settings.Log.Disabled {
		t.Fatalf("expected log disabled, got: %+v", settings.Log)
	}
	if len(settings.Log.Classes) != 2 || settings.Log.Classes[0] != "denial" || settings.Log.Classes[1] != "error" {
		t.Fatalf("expected log classes [denial error], got: %+v", settings.Log.Classes)
	}
	if !settings.Errors.ConsolidateDisabled {
		t.Fatalf("expected errors consolidate disabled, got: %+v", settings.Errors)
	}
	if !settings.BufsizeDisabled {
		t.Fatalf("expected bufsize disabled, got: %+v", settings.BufsizeDisabled)
	}

	// Blank/unchecked fields clear previously-set tuning back to zero-value
	// defaults (both cache knobs enabled, log unconditional, errors consolidated,
	// bufsize enabled).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", base(nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save (clear tuning) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	settings, err = st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Cache.ServeStaleDisabled || settings.Cache.PrefetchDisabled {
		t.Fatalf("expected cache knobs cleared, got: %+v", settings.Cache)
	}
	if settings.Log.Disabled || len(settings.Log.Classes) != 0 {
		t.Fatalf("expected log tuning cleared, got: %+v", settings.Log)
	}
	if settings.Errors.ConsolidateDisabled {
		t.Fatalf("expected errors consolidate re-enabled, got: %+v", settings.Errors)
	}
	if settings.BufsizeDisabled {
		t.Fatalf("expected bufsize re-enabled, got: %+v", settings.BufsizeDisabled)
	}
}

// The mode select is an explicit choice (none/default/all/custom) rather
// than inferring "none" from a blank text field — a blank field and a
// never-touched field were otherwise indistinguishable, which made saving
// "none" look like it silently turned into "default" on the next render.
func TestSettingsSave_MDNSServiceTypesMode(t *testing.T) {
	svc, st, _ := newService(t)
	h := NewServer(svc, ":0").Handler

	post := func(mode, custom string) {
		t.Helper()
		form := url.Values{
			"upstream_address": {"10.0.0.5:53"}, "upstream_protocol": {"plain"},
			"plain_enabled": {"on"}, "plain_port": {"53"},
			"acme_renew_before_days":  {"30"},
			"mdns_service_types_mode": {mode},
		}
		if custom != "" {
			form.Set("mdns_service_types", custom)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, hxRequest(t, http.MethodPost, "/settings", form))
		if rec.Code != http.StatusOK {
			t.Fatalf("save (mode=%s) status = %d, body = %s", mode, rec.Code, rec.Body.String())
		}
	}
	loadServiceTypes := func() []string {
		t.Helper()
		settings, err := st.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		return settings.MDNS.ServiceTypes
	}

	post("none", "")
	if got := loadServiceTypes(); len(got) != 0 {
		t.Errorf("mode=none: ServiceTypes = %v, want empty", got)
	}

	post("default", "")
	if got := loadServiceTypes(); len(got) != 1 || got[0] != "default" {
		t.Errorf("mode=default: ServiceTypes = %v, want [default]", got)
	}

	post("all", "")
	if got := loadServiceTypes(); len(got) != 1 || got[0] != "all" {
		t.Errorf("mode=all: ServiceTypes = %v, want [all]", got)
	}

	post("custom", "_http._tcp, _ipp._tcp")
	if got := loadServiceTypes(); len(got) != 2 || got[0] != "_http._tcp" || got[1] != "_ipp._tcp" {
		t.Errorf("mode=custom: ServiceTypes = %v, want [_http._tcp _ipp._tcp]", got)
	}
}

func TestMDNSPageDisabledByDefault(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hxRequest(t, http.MethodGet, "/mdns", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "disabled") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestStaticAssetsServed(t *testing.T) {
	h := newTestServer(t)
	for _, path := range []string{"/static/style.css", "/static/js/htmx.min.js", "/static/js/theme.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
}
