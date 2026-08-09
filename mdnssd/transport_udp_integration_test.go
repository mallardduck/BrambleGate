//go:build mdns_integration

// A real end-to-end test: announce a service over real multicast and browse
// for it with a real udpTransport. Multicast/mDNS is timing- and
// environment-sensitive, so this is gated behind the mdns_integration tag
// rather than in the default suite.
//
// The announcer below is intentionally minimal and hand-rolled rather than
// borrowed from another mDNS library (e.g. brutella/dnssd): mdnssd exists to
// replace that dependency (see doc.go), so it must not need it — not even
// in its own tests. It reuses nothing but primitives this package already
// owns: miekg/dns for message construction and the Transport this file is
// testing for sending.
//
//	go test -tags mdns_integration -run TestUDPTransport -v ./mdnssd/...
package mdnssd

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// announce sends msg on transport immediately and then every interval,
// until ctx is done. Standing in for a real advertiser.
func announce(ctx context.Context, transport Transport, msg *dns.Msg, interval time.Duration) {
	_ = transport.SendQuery("", msg)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = transport.SendQuery("", msg)
		}
	}
}

// bundleResponse builds a full PTR+SRV+TXT+(A|AAAA) mDNS response for one
// service instance — the same bundle a real responder sends when answering
// a browse query.
func bundleResponse(question, instance, host string, port int, txt map[string]string, ttl time.Duration, ip net.IP) *dns.Msg {
	ttlSec := uint32(ttl.Seconds())
	hdr := func(name string, rrtype uint16) dns.RR_Header {
		return dns.RR_Header{Name: name, Rrtype: rrtype, Class: dns.ClassINET, Ttl: ttlSec}
	}

	m := new(dns.Msg)
	m.Response = true
	m.Answer = []dns.RR{
		&dns.PTR{Hdr: hdr(question, dns.TypePTR), Ptr: instance},
		&dns.SRV{Hdr: hdr(instance, dns.TypeSRV), Target: host, Port: uint16(port)},
	}
	if len(txt) > 0 {
		segs := make([]string, 0, len(txt))
		for k, v := range txt {
			segs = append(segs, k+"="+v)
		}
		m.Answer = append(m.Answer, &dns.TXT{Hdr: hdr(instance, dns.TypeTXT), Txt: segs})
	}
	if ip4 := ip.To4(); ip4 != nil {
		m.Answer = append(m.Answer, &dns.A{Hdr: hdr(host, dns.TypeA), A: ip4})
	} else {
		m.Answer = append(m.Answer, &dns.AAAA{Hdr: hdr(host, dns.TypeAAAA), AAAA: ip})
	}
	return m
}

func TestUDPTransport_DiscoversRealAdvertisement(t *testing.T) {
	transport, err := NewUDPTransport(nil)
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const question = "_http._tcp.local."
	const instance = "mdnssdtest-printer._http._tcp.local."
	bundle := bundleResponse(question, instance, "mdnssdtest-printer.local.", 8080,
		map[string]string{"txtv": "1"}, 30*time.Second, net.ParseIP("127.0.0.1"))
	go announce(ctx, transport, bundle, 2*time.Second)

	b := New(WithTransport(transport))

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
