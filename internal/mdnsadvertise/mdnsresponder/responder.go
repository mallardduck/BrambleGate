// Package mdnsresponder wraps dnssd.Responder to advertise mDNS-SD services.
package mdnsresponder

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/brutella/dnssd"
)

// ServiceSpec describes a service to advertise.
type ServiceSpec struct {
	Name string // e.g., "BrambleGate"
	Type string // e.g., "_domain._udp.local"
	Port uint16
	TXT  map[string]string
}

// Handle is an opaque handle to a registered service (wraps dnssd.ServiceHandle).
type Handle any

// Backend abstracts mDNS service advertisement.
type Backend interface {
	// Register advertises a service. Returns an opaque Handle to be used with Unregister.
	Register(ServiceSpec) (Handle, error)
	// Unregister stops advertising a service.
	Unregister(Handle) error
	// Close closes the backend and sends goodbye packets for all registered services.
	Close() error
}

// New creates a Backend backed by dnssd. It starts a background Respond goroutine
// that runs for the Backend's lifetime until Close is called.
func New(ctx context.Context, log *slog.Logger) (Backend, error) {
	responder, err := dnssd.NewResponder()
	if err != nil {
		return nil, fmt.Errorf("create responder: %w", err)
	}

	// Create a context that we control (independent of the caller's ctx).
	respondCtx, respondCancel := context.WithCancel(context.Background())

	// Start the responder in a background goroutine.
	respondErrChan := make(chan error, 1)
	go func() {
		respondErrChan <- responder.Respond(respondCtx)
	}()

	return &backend{
		responder:      responder,
		respondCtx:     respondCtx,
		respondCancel:  respondCancel,
		respondErrChan: respondErrChan,
		log:            log,
	}, nil
}

type backend struct {
	responder      dnssd.Responder
	respondCtx     context.Context
	respondCancel  context.CancelFunc
	respondErrChan chan error
	log            *slog.Logger
}

// Register implements Backend.
func (b *backend) Register(spec ServiceSpec) (Handle, error) {
	svc, err := dnssd.NewService(dnssd.Config{
		Name:   spec.Name,
		Type:   spec.Type,
		Domain: "local",
		Port:   int(spec.Port),
		Text:   spec.TXT,
	})
	if err != nil {
		return nil, fmt.Errorf("create service: %w", err)
	}

	handle, err := b.responder.Add(svc)
	if err != nil {
		return nil, fmt.Errorf("register service: %w", err)
	}

	return handle, nil
}

// Unregister implements Backend.
func (b *backend) Unregister(h Handle) error {
	handle, ok := h.(dnssd.ServiceHandle)
	if !ok {
		return fmt.Errorf("invalid handle type")
	}
	b.responder.Remove(handle)
	return nil
}

// Close implements Backend. It cancels the Respond context (triggering goodbye
// packets) and waits for the Respond goroutine to finish.
func (b *backend) Close() error {
	b.respondCancel()
	// Wait for Respond to exit and log any error (don't fail — we're closing anyway).
	err := <-b.respondErrChan
	if err != nil && err != context.Canceled {
		b.log.Warn("mdns responder error on close", "err", err)
	}
	return nil
}
