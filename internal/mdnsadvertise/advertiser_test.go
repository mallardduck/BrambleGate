package mdnsadvertise

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/joshuafuller/beacon/responder"

	"github.com/mallardduck/BrambleGate/model"
)

// fakeBackend records Register/Unregister calls instead of touching real mDNS
// multicast sockets.
type fakeBackend struct {
	mu           sync.Mutex
	registered   []*responder.Service
	unregistered []string
	failRegister map[string]bool // ServiceType -> force Register to fail once

	calls chan string // one entry per Register/Unregister call, for waitForCall
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{calls: make(chan string, 16), failRegister: map[string]bool{}}
}

func (f *fakeBackend) Register(svc *responder.Service) error {
	f.mu.Lock()
	fail := f.failRegister[svc.ServiceType]
	if !fail {
		f.registered = append(f.registered, svc)
	}
	f.mu.Unlock()
	f.calls <- "register:" + svc.ServiceType
	if fail {
		return errors.New("simulated register failure")
	}
	return nil
}

func (f *fakeBackend) Unregister(serviceID string) error {
	f.mu.Lock()
	f.unregistered = append(f.unregistered, serviceID)
	f.mu.Unlock()
	f.calls <- "unregister:" + serviceID
	return nil
}

func (f *fakeBackend) Close() error { return nil }

func (f *fakeBackend) waitForCall(t *testing.T) string {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a backend call")
		return ""
	}
}

func newTestAdvertiser(t *testing.T) (*Advertiser, *fakeBackend) {
	t.Helper()
	fb := newFakeBackend()
	orig := newResponder
	newResponder = func(context.Context, *slog.Logger) (responderBackend, error) { return fb, nil }
	t.Cleanup(func() { newResponder = orig })

	log := slog.New(slog.DiscardHandler)
	a, err := New(t.Context(), log)
	if err != nil {
		t.Fatal(err)
	}
	return a, fb
}

func plainSettings(port int) model.Settings {
	return model.Settings{Listeners: model.Listeners{Plain: model.Listener{Enabled: true, Port: port}}}
}

func TestReconcileRegistersDomainServicesWhenPlainEnabled(t *testing.T) {
	a, fb := newTestAdvertiser(t)
	a.Reconcile(plainSettings(53))

	seen := map[string]bool{}
	for range 2 {
		seen[fb.waitForCall(t)] = true
	}
	if !seen["register:_domain._udp.local"] || !seen["register:_domain._tcp.local"] {
		t.Fatalf("expected both _domain service types registered, got %v", seen)
	}
}

func TestReconcileAddsDoTWithoutTouchingDomain(t *testing.T) {
	a, fb := newTestAdvertiser(t)
	settings := plainSettings(53)
	a.Reconcile(settings)
	fb.waitForCall(t)
	fb.waitForCall(t)

	settings.Listeners.DoT = model.Listener{Enabled: true, Port: 853}
	settings.ACME.Domain = "dns.example.com"
	a.Reconcile(settings)

	call := fb.waitForCall(t)
	if call != "register:_dot._tcp.local" {
		t.Fatalf("expected _dot._tcp registration, got %q", call)
	}

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.unregistered) != 0 {
		t.Fatalf("expected no unregistrations when only adding DoT, got %v", fb.unregistered)
	}
	var dot *responder.Service
	for _, svc := range fb.registered {
		if svc.ServiceType == "_dot._tcp.local" {
			dot = svc
		}
	}
	if dot == nil {
		t.Fatal("dot service was not recorded as registered")
	}
	if dot.TXTRecords["domain"] != "dns.example.com" {
		t.Fatalf("expected domain TXT key, got %v", dot.TXTRecords)
	}
}

func TestReconcileUnregistersOnlyDomainWhenPlainDisabled(t *testing.T) {
	a, fb := newTestAdvertiser(t)
	settings := plainSettings(53)
	settings.Listeners.DoT = model.Listener{Enabled: true, Port: 853}
	a.Reconcile(settings)
	fb.waitForCall(t)
	fb.waitForCall(t)
	fb.waitForCall(t)

	settings.Listeners.Plain.Enabled = false
	a.Reconcile(settings)

	seen := map[string]bool{}
	for range 2 {
		seen[fb.waitForCall(t)] = true
	}
	if !seen["unregister:BrambleGate-domain-udp._domain._udp.local"] || !seen["unregister:BrambleGate-domain-tcp._domain._tcp.local"] {
		t.Fatalf("expected both _domain service types unregistered, got %v", seen)
	}

	fb.mu.Lock()
	defer fb.mu.Unlock()
	for _, id := range fb.unregistered {
		if id == "BrambleGate-dot-tcp._dot._tcp.local" {
			t.Fatal("_dot._tcp should not have been unregistered")
		}
	}
}

func TestCloseClosesBackend(t *testing.T) {
	a, _ := newTestAdvertiser(t)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestReconcileIsNoOpWhenNothingChanged(t *testing.T) {
	a, fb := newTestAdvertiser(t)
	settings := plainSettings(53)
	a.Reconcile(settings)
	fb.waitForCall(t)
	fb.waitForCall(t)

	a.Reconcile(settings) // same settings again

	select {
	case call := <-fb.calls:
		t.Fatalf("expected no further backend calls, got %q", call)
	case <-time.After(200 * time.Millisecond):
	}
}
