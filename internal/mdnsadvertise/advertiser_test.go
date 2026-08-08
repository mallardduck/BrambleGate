package mdnsadvertise

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mallardduck/BrambleGate/internal/mdnsadvertise/mdnsresponder"
	"github.com/mallardduck/BrambleGate/model"
)

// fakeBackend records Register/Unregister calls instead of touching real mDNS
// multicast sockets.
type fakeBackend struct {
	mu           sync.Mutex
	registered   []mdnsresponder.ServiceSpec
	unregistered []string
	failRegister map[string]bool // Type -> force Register to fail once

	calls chan string // one entry per Register/Unregister call, for waitForCall
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{calls: make(chan string, 16), failRegister: map[string]bool{}}
}

func (f *fakeBackend) Register(spec mdnsresponder.ServiceSpec) (mdnsresponder.Handle, error) {
	f.mu.Lock()
	fail := f.failRegister[spec.Type]
	if !fail {
		f.registered = append(f.registered, spec)
	}
	f.mu.Unlock()
	f.calls <- "register:" + spec.Type
	if fail {
		return nil, errors.New("simulated register failure")
	}
	// Use the service type itself as the opaque handle for the fake
	return spec.Type, nil
}

func (f *fakeBackend) Unregister(h mdnsresponder.Handle) error {
	serviceType, ok := h.(string)
	if !ok {
		return errors.New("invalid handle type")
	}
	f.mu.Lock()
	f.unregistered = append(f.unregistered, serviceType)
	f.mu.Unlock()
	f.calls <- "unregister:" + serviceType
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
	var dot *mdnsresponder.ServiceSpec
	for i, spec := range fb.registered {
		if spec.Type == "_dot._tcp.local" {
			dot = &fb.registered[i]
		}
	}
	if dot == nil {
		t.Fatal("dot service was not recorded as registered")
	}
	if dot.TXT["domain"] != "dns.example.com" {
		t.Fatalf("expected domain TXT key, got %v", dot.TXT)
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
	if !seen["unregister:_domain._udp.local"] || !seen["unregister:_domain._tcp.local"] {
		t.Fatalf("expected both _domain service types unregistered, got %v", seen)
	}

	fb.mu.Lock()
	defer fb.mu.Unlock()
	for _, id := range fb.unregistered {
		if id == "_dot._tcp.local" {
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
