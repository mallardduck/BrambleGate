//go:build mdns_integration

// A real end-to-end mDNS test: advertise a service with dnssd's responder, run
// the Listener, and confirm the discovery lands in the Table as a published,
// resolvable entry. Multicast/mDNS is timing- and environment-sensitive, so this
// is gated behind the mdns_integration tag rather than in the default suite.
//
//	go test -tags mdns_integration -run TestListenerDiscovers -v ./plugins/mdnsbridge/...
package mdnsbridge

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/brutella/dnssd"
)

func TestListenerDiscovers(t *testing.T) {
	// Advertise a specific test instance on the local network using dnssd.
	responderCtx, responderCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer responderCancel()

	responder, err := dnssd.NewResponder()
	if err != nil {
		t.Fatalf("responder init: %v", err)
	}
	// dnssd.Responder has no Close method — responderCancel (deferred above)
	// stops it via ctx cancellation instead.

	// Start the responder in a background goroutine.
	responderErrChan := make(chan error, 1)
	go func() {
		responderErrChan <- responder.Respond(responderCtx)
	}()

	testService, err := dnssd.NewService(dnssd.Config{
		Name:   "brambletest-printer",
		Type:   "_http._tcp",
		Domain: "local",
		Host:   "brambletest-printer",
		Port:   8080,
		Text:   map[string]string{"txtv": "1"},
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	handle, err := responder.Add(testService)
	if err != nil {
		t.Fatalf("register service: %v", err)
	}
	defer responder.Remove(handle)

	// auto-publish everything (match-all) so any discovery is immediately servable
	table := NewTable(Config{DefaultSuffix: "home.arpa", AutoPublish: SelectorSet{{}}}, time.Minute)
	l := NewListener(table, []string{"_http._tcp"}, nil, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go l.Run(ctx)

	// Poll the table until the advertised instance shows up (mapped into home.arpa).
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range table.Snapshot() {
			// Look for the specific instance we advertised, not just any service.
			if e.Published && e.Service == "_http._tcp" && e.Instance == "brambletest-printer" &&
				len(e.IPv4)+len(e.IPv6) > 0 {
				t.Logf("discovered %s (host %s, service %s) -> v4=%v v6=%v",
					e.Name, e.Host, e.Service, e.IPv4, e.IPv6)
				return // success
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("advertised service was not discovered within the timeout")
}
