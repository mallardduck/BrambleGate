package gui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func TestAPIPluginsListsRegisteredComponents(t *testing.T) {
	pluginreg.Register(pluginreg.Descriptor{Name: "test-api-plugin", Kind: pluginreg.BrambleOnly})
	pluginreg.SetLoaded("test-api-plugin", true, "")

	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/plugins", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content type, got %q", ct)
	}

	var body struct {
		Plugins []pluginreg.Entry `json:"plugins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v, body=%s", err, rec.Body.String())
	}
	found := false
	for _, e := range body.Plugins {
		if e.Name == "test-api-plugin" {
			found = true
			if !e.Loaded {
				t.Fatal("expected test-api-plugin to report Loaded")
			}
		}
	}
	if !found {
		t.Fatal("expected test-api-plugin in /api/plugins response")
	}
}

func TestStartAdvertiseReportsLoaded(t *testing.T) {
	svc, _, _ := newService(t)
	stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StartAdvertise(settings); err != nil {
		t.Fatalf("StartAdvertise: %v", err)
	}

	if !pluginreg.Loaded("mdnsadvertise") {
		t.Fatal("expected mdnsadvertise to report Loaded after StartAdvertise")
	}
}

func TestStopAdvertiseReportsNotLoaded(t *testing.T) {
	svc, _, _ := newService(t)
	stubMDNSAdvertiser(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StartAdvertise(settings); err != nil {
		t.Fatalf("StartAdvertise: %v", err)
	}
	svc.StopAdvertise()

	if pluginreg.Loaded("mdnsadvertise") {
		t.Fatal("expected mdnsadvertise to report not-Loaded after StopAdvertise")
	}
	_, s, ok := pluginreg.Get("mdnsadvertise")
	if !ok {
		t.Fatal("expected mdnsadvertise to be registered")
	}
	if s.Reason == "" {
		t.Fatal("expected a non-empty reason after StopAdvertise")
	}
}

// TestReloadReportsHostsLoaded: the hosts stanza is unconditionally present
// in every rendered Corefile (dev-docs/static-hosts.md) — it has no
// setup() of its own to call pluginreg.SetLoaded from (plugins/hosts/doc.go
// explains why), so every successful reload path reports it directly.
// SaveSettings is as good a trigger as any of them.
func TestReloadReportsHostsLoaded(t *testing.T) {
	svc, _, _ := newService(t)

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if !pluginreg.Loaded("hosts") {
		t.Fatal("expected hosts to report Loaded after a successful reload")
	}
	d, _, ok := pluginreg.Get("hosts")
	if !ok {
		t.Fatal("expected hosts to be registered")
	}
	if d.Kind != pluginreg.CoreDNSPlugin {
		t.Fatalf("expected hosts Kind to be CoreDNSPlugin, got %q", d.Kind)
	}
}

func TestStartAdvertiseFailureReportsReason(t *testing.T) {
	svc, _, _ := newService(t)

	orig := newMDNSAdvertiser
	newMDNSAdvertiser = func(context.Context, *slog.Logger) (mdnsAdvertiser, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { newMDNSAdvertiser = orig })

	settings, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StartAdvertise(settings); err == nil {
		t.Fatal("expected StartAdvertise to fail")
	}

	if pluginreg.Loaded("mdnsadvertise") {
		t.Fatal("expected mdnsadvertise to report not-Loaded after a failed start")
	}
	_, s, ok := pluginreg.Get("mdnsadvertise")
	if !ok {
		t.Fatal("expected mdnsadvertise to be registered")
	}
	if s.Reason == "" {
		t.Fatal("expected a non-empty failure reason")
	}
}
