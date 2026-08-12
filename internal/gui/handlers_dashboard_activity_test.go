package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/plugins/querylog"
)

// enableQueryLog turns Query Log on via the real Service.SaveSettings path
// — which, since Phase 7b, also opens a real querylog.Store (configgen.
// QueryLogStoreConfig doesn't distinguish "ring only" from "ring+store";
// see model.QueryLog.Enabled's doc comment). That Store is a process-wide
// singleton (plugins/querylog/store_lifecycle.go) that would otherwise
// leak its open file/background goroutines across every other test in
// this package, so every caller gets an automatic cleanup.
func enableQueryLog(t *testing.T, h *Service) {
	t.Helper()
	t.Cleanup(func() { _ = querylog.ReconcileStore(querylog.StoreConfig{}) })
	settings, err := h.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.QueryLog.Enabled = true
	if err := h.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardActivity_DisabledShowsCTA(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "Enable Query Log") {
		t.Fatalf("expected the Activity CTA when Query Log is disabled, got: %s", body)
	}
	if strings.Contains(body, `id="dashboard-activity"`) {
		t.Fatalf("did not expect the Activity section markup when Query Log is disabled, got: %s", body)
	}
	if strings.Contains(body, "chart.umd.min.js") {
		t.Fatalf("did not expect Chart.js to load on a page with Query Log disabled, got: %s", body)
	}
}

func TestDashboardActivity_EnabledLoadsChartJS(t *testing.T) {
	svc, _, _ := newService(t)
	enableQueryLog(t, svc)
	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `id="dashboard-activity"`) {
		t.Fatalf("expected the Activity section once Query Log is enabled, got: %s", body)
	}
	if !strings.Contains(body, "chart.umd.min.js") || !strings.Contains(body, "charts.js") {
		t.Fatalf("expected Chart.js + charts.js to load once Query Log is enabled, got: %s", body)
	}
}

func TestDashboardActivity_EnabledNoStore_ShowsPersistenceNote(t *testing.T) {
	svc, _, _ := newService(t)
	enableQueryLog(t, svc) // also opens a real Store, per its own doc comment
	// Force the Store back closed to simulate the (normally transient/
	// error-only) state of Query Log being on but persistence not actually
	// configured — dashboardActivityData must degrade gracefully rather
	// than erroring when this happens (e.g. OpenStore failed on a disk
	// error at startup; internal/cli logs a warning but doesn't block).
	if err := querylog.ReconcileStore(querylog.StoreConfig{}); err != nil {
		t.Fatalf("ReconcileStore (force-close): %v", err)
	}
	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "Total queries") {
		t.Fatalf("expected the cheap-rollup tiles to render even with no Store configured, got: %s", body)
	}
	if strings.Count(body, "Enable Query Log persistence") != 3 {
		t.Fatalf("expected the client-activity, top-domains and top-clients cards to show the persistence note, got: %s", body)
	}
}

func TestDashboardActivity_WithStore_ShowsTopDomains(t *testing.T) {
	svc, _, _ := newService(t)
	// enableQueryLog opens a real Store at the Service's own configDir
	// (Phase 7b wiring) — record straight into that, rather than
	// pre-opening a separate Store at an unrelated path, which
	// enableQueryLog's own SaveSettings call would immediately close and
	// replace anyway (a genuine path change, per ReconcileStore's rules).
	enableQueryLog(t, svc)
	store := querylog.CurrentStore()
	if store == nil {
		t.Fatal("expected enableQueryLog to have opened a Store")
	}
	// The auto-opened Store uses model.QueryLog's default flush interval
	// (2s, since settings.QueryLog.FlushIntervalSeconds is unset here) —
	// shrink it so the test doesn't need to wait that long.
	store.SetTuning(0, 0, 10*time.Millisecond)
	store.Record(querylog.Entry{QName: "nas.home.arpa.", Client: querylog.ClientInfo{IP: "10.0.0.5", VLAN: "trusted"}, Timestamp: time.Now()})
	store.Record(querylog.Entry{QName: "nas.home.arpa.", Client: querylog.ClientInfo{IP: "10.0.0.5", VLAN: "trusted"}, Timestamp: time.Now()})
	waitForStoreRows(t, store)

	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "nas.home.arpa.") {
		t.Fatalf("expected nas.home.arpa. in the top-domains card, got: %s", body)
	}
	if !strings.Contains(body, "10.0.0.5") {
		t.Fatalf("expected 10.0.0.5 in the top-clients card, got: %s", body)
	}
	if strings.Contains(body, "Enable Query Log persistence") {
		t.Fatalf("did not expect the persistence note once Store is configured, got: %s", body)
	}
}

func TestDashboardActivityFragment_ReturnsFragmentOnly(t *testing.T) {
	svc, _, _ := newService(t)
	enableQueryLog(t, svc)
	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dashboard/activity", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "BrambleGate</title>") {
		t.Fatalf("expected a bare fragment (no layout chrome), got: %s", body)
	}
	if !strings.Contains(body, `id="dashboard-activity"`) {
		t.Fatalf("expected the Activity section markup, got: %s", body)
	}
}

func TestDashboardActivityFragment_DisabledStillRendersDashboard(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dashboard/activity", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestQueryLogStats_ReturnsTotalsJSON: a debug/troubleshooting endpoint
// (server.go's queryLogStats) exposing the exact same Totals the Dashboard's
// Activity tiles/charts render, as raw JSON — so a discrepancy between what
// a chart shows and what the underlying counters hold can be checked
// directly instead of trusting chart rendering/legend reading.
func TestQueryLogStats_ReturnsTotalsJSON(t *testing.T) {
	svc, _, _ := newService(t)
	enableQueryLog(t, svc)
	h := NewServer(svc, ":0").Handler

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/querylog/stats", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got querylog.Totals
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response body isn't valid querylog.Totals JSON: %v (body: %s)", err, rec.Body.String())
	}
}

// waitForStoreRows gives the store's buffered writer (plugins/querylog/
// store.go, FlushInterval: 10ms in these tests) time to flush before the
// dashboard reads it. Store doesn't expose a row-count accessor outside
// plugins/querylog, so this is a bounded sleep rather than a real poll —
// generous relative to the configured flush interval to avoid flakiness.
func waitForStoreRows(t *testing.T, s *querylog.Store) {
	t.Helper()
	if s.Dropped() > 0 {
		t.Fatalf("store dropped entries unexpectedly")
	}
	time.Sleep(100 * time.Millisecond)
}
