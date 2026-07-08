package gui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
