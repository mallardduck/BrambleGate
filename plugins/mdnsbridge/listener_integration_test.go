//go:build mdns_integration

// A real end-to-end mDNS test: advertise a service with zeroconf, run the
// Listener, and confirm the discovery lands in the Table as a published,
// resolvable entry. Multicast/mDNS is timing- and environment-sensitive, so this
// is gated behind the mdns_integration tag rather than in the default suite.
//
//	go test -tags mdns_integration -run TestListenerDiscovers -v ./plugins/mdnsbridge/...
package mdnsbridge

import (
	"context"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestListenerDiscovers(t *testing.T) {
	// Advertise a fake service instance on the local network.
	server, err := zeroconf.Register(
		"brambletest-printer", // instance
		"_http._tcp",          // service
		"local.",              // domain
		8080,
		[]string{"txtv=1"},
		nil, // all interfaces
	)
	if err != nil {
		t.Fatalf("advertise: %v", err)
	}
	defer server.Shutdown()

	// auto-publish everything (match-all) so any discovery is immediately servable
	table := NewTable(Config{DefaultSuffix: "home.arpa", AutoPublish: SelectorSet{{}}}, time.Minute)
	l := NewListener(table, []string{"_http._tcp"}, nil, discardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go l.Run(ctx)

	// Poll the table until the advertised host shows up (mapped into home.arpa).
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range table.Snapshot() {
			if e.Published && len(e.IPv4)+len(e.IPv6) > 0 {
				t.Logf("discovered %s (host %s, service %s) -> v4=%v v6=%v",
					e.Name, e.Host, e.Service, e.IPv4, e.IPv6)
				return // success
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("advertised service was not discovered within the timeout")
}
