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
