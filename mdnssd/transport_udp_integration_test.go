//go:build mdns_integration

// A real end-to-end test: advertise a service with dnssd's responder (the
// advertise side stays on dnssd — see doc.go), then browse for it over a
// real udpTransport. Multicast/mDNS is timing- and environment-sensitive,
// so this is gated behind the mdns_integration tag rather than in the
// default suite — mirrors plugins/mdnsbridge/listener_integration_test.go.
//
//	go test -tags mdns_integration -run TestUDPTransport -v ./mdnssd/...
package mdnssd

import (
	"context"
	"testing"
	"time"

	"github.com/brutella/dnssd"
)

func TestUDPTransport_DiscoversRealAdvertisement(t *testing.T) {
	responderCtx, responderCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer responderCancel()

	responder, err := dnssd.NewResponder()
	if err != nil {
		t.Fatalf("responder init: %v", err)
	}
	// dnssd.Responder has no Close method — responderCancel (deferred above)
	// stops it via ctx cancellation instead.

	responderErrChan := make(chan error, 1)
	go func() { responderErrChan <- responder.Respond(responderCtx) }()

	svc, err := dnssd.NewService(dnssd.Config{
		Name:   "mdnssdtest-printer",
		Type:   "_http._tcp",
		Domain: "local",
		Host:   "mdnssdtest-printer",
		Port:   8080,
		Text:   map[string]string{"txtv": "1"},
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	handle, err := responder.Add(svc)
	if err != nil {
		t.Fatalf("register service: %v", err)
	}
	defer responder.Remove(handle)

	transport, err := NewUDPTransport(nil)
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer func() { _ = transport.Close() }()

	b := New(WithTransport(transport))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	found := make(chan Entry, 8)
	go func() {
		_ = b.Browse(ctx, "_http._tcp", nil, func(e Entry) { found <- e }, func(Entry) {})
	}()

	deadline := time.After(18 * time.Second)
	for {
		select {
		case e := <-found:
			t.Logf("discovered %+v", e)
			if e.Instance == "mdnssdtest-printer" && len(e.IPv4)+len(e.IPv6) > 0 {
				return // success
			}
		case <-deadline:
			t.Fatal("advertised service was not discovered within the timeout")
		}
	}
}
