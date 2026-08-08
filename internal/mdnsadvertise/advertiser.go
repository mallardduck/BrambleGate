// Package mdnsadvertise self-advertises this server's own DNS service(s) via
// mDNS-SD (RFC 6763), independent of mdnsbridge's discovery side (which browses
// for OTHER devices). It is owned by the host process for its whole lifetime,
// started/stopped/reconfigured as mdns.advertise.enabled and the DNS listener
// settings change — see internal/gui/service.go's StartAdvertise/StopAdvertise.
package mdnsadvertise

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/joshuafuller/beacon/responder"

	"github.com/mallardduck/BrambleGate/model"
)

// responderBackend is the slice of *responder.Responder the Advertiser needs.
// Kept as an interface so tests can supply a fake instead of opening real
// mDNS multicast sockets (beacon's own MockTransport is an unexported internal
// type, not importable outside its module).
type responderBackend interface {
	Register(*responder.Service) error
	Unregister(serviceID string) error
	Close() error
}

// newResponder constructs the real backend. Overridable so tests never touch
// real sockets — the same seam pattern as gui.runMDNSListener.
var newResponder = func(ctx context.Context, _ *slog.Logger) (responderBackend, error) {
	// TODO maybe we need to call: responder.WithHostname()
	return responder.New(ctx)
}

// registeredService is what Advertiser tracks per service type so Reconcile can
// detect a port/TXT change (which requires unregister+re-register) versus no
// change at all (a no-op).
type registeredService struct {
	port uint16
	txt  string // fingerprint of the TXT records, for change detection
}

// Advertiser owns the registered mDNS-SD service set for this process.
type Advertiser struct {
	backend responderBackend
	log     *slog.Logger

	mu      sync.Mutex
	tracked map[string]registeredService // serviceType -> what's registered/in-flight
}

// New creates an Advertiser. Nothing is registered until Reconcile is called.
func New(ctx context.Context, log *slog.Logger) (*Advertiser, error) {
	backend, err := newResponder(ctx, log)
	if err != nil {
		return nil, fmt.Errorf("start mdns responder: %w", err)
	}
	log.Info("mdns advertise: responder started")
	return &Advertiser{backend: backend, log: log, tracked: make(map[string]registeredService)}, nil
}

// Reconcile brings the advertised service set in line with settings: registers
// newly-wanted services, unregisters no-longer-wanted ones, and re-registers
// ones whose port or TXT content changed. Register/Unregister each block for
// ~1.75s (RFC 6762 probing/announcing), so the actual work runs in goroutines —
// Reconcile itself returns immediately.
func (a *Advertiser) Reconcile(settings model.Settings) {
	wanted := desiredServices(settings)

	wantedTypes := make(map[string]*responder.Service, len(wanted))
	types := make([]string, 0, len(wanted))
	for _, svc := range wanted {
		wantedTypes[svc.ServiceType] = svc
		types = append(types, svc.ServiceType)
	}
	if len(types) == 0 {
		a.log.Info("mdns advertise: reconcile — nothing to advertise (no enabled listener maps to an mDNS-SD service type)")
	} else {
		a.log.Info("mdns advertise: reconcile", "wanted_service_types", types)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for serviceType := range a.tracked {
		if _, stillWanted := wantedTypes[serviceType]; !stillWanted {
			a.unregisterLocked(serviceType)
		}
	}

	for serviceType, svc := range wantedTypes {
		want := registeredService{port: svc.Port, txt: txtFingerprint(svc.TXTRecords)}
		if current, ok := a.tracked[serviceType]; ok {
			if current == want {
				continue // already registered with these exact params
			}
			a.unregisterLocked(serviceType) // params changed: drop and re-register below
		}
		a.registerLocked(svc, want)
	}
}

// registerLocked marks serviceType as tracked and spawns the (blocking)
// Register call. Called with a.mu held.
func (a *Advertiser) registerLocked(svc *responder.Service, want registeredService) {
	a.tracked[svc.ServiceType] = want
	a.log.Info("mdns advertise: registering service",
		"service_type", svc.ServiceType, "instance_name", svc.InstanceName, "port", svc.Port)
	go func() {
		if err := a.backend.Register(svc); err != nil {
			a.log.Error("mdns advertise: register failed", "service_type", svc.ServiceType, "err", err)
			a.mu.Lock()
			delete(a.tracked, svc.ServiceType) // let the next Reconcile retry
			a.mu.Unlock()
			return
		}
		a.log.Info("mdns advertise: service registered — should now be visible in an mDNS browser",
			"service_type", svc.ServiceType, "instance_name", svc.InstanceName, "port", svc.Port)
	}()
}

// unregisterLocked drops serviceType from tracking and spawns the (blocking)
// Unregister call. Called with a.mu held.
func (a *Advertiser) unregisterLocked(serviceType string) {
	delete(a.tracked, serviceType)
	id := instanceNameFor(serviceType) + "." + serviceType
	a.log.Info("mdns advertise: unregistering service", "service_type", serviceType)
	go func() {
		if err := a.backend.Unregister(id); err != nil {
			a.log.Error("mdns advertise: unregister failed", "service_type", serviceType, "err", err)
			return
		}
		a.log.Info("mdns advertise: service unregistered", "service_type", serviceType)
	}()
}

// Close unregisters everything (goodbye packets) and closes the backend.
func (a *Advertiser) Close() error {
	a.log.Info("mdns advertise: stopping responder")
	return a.backend.Close()
}

// txtFingerprint turns a TXT map into a stable string for change detection —
// map iteration order isn't, so this can't just be the map itself.
func txtFingerprint(txt map[string]string) string {
	keys := make([]string, 0, len(txt))
	for k := range txt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(txt[k])
		b.WriteByte(';')
	}
	return b.String()
}
