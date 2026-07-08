//go:build mdns_integration

// A real end-to-end mDNS test: advertise a service with beacon's responder, run
// the Listener, and confirm the discovery lands in the Table as a published,
// resolvable entry. Multicast/mDNS is timing- and environment-sensitive, so this
// is gated behind the mdns_integration tag rather than in the default suite.
//
//	go test -tags mdns_integration -run TestListenerDiscovers -v ./plugins/mdnsbridge/...
package mdnsbridge

import (
	"context"
	"testing"
	"time"

	"github.com/joshuafuller/beacon/responder"
)

func TestListenerDiscovers(t *testing.T) {
	// Advertise a fake service instance on the local network.
	regCtx, regCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer regCancel()

	r, err := responder.New(regCtx)
	if err != nil {
		t.Fatalf("responder init: %v", err)
	}
	defer func() { _ = r.Close() }()

	svc := &responder.Service{
		InstanceName: "brambletest-printer",
		ServiceType:  "_http._tcp.local",
		Port:         8080,
		TXTRecords:   map[string]string{"txtv": "1"},
	}
	if err := r.Register(svc); err != nil {
		t.Fatalf("advertise: %v", err)
	}
	defer func() { _ = r.Unregister("brambletest-printer._http._tcp.local") }()

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
