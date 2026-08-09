package mdnssd

import (
	"context"
	"errors"
	"net"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	addrIPv4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	addrIPv6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}
)

var errNoMulticastIfaces = errors.New("mdnssd: no usable multicast interfaces found")
var errNoUsableSocket = errors.New("mdnssd: neither IPv4 nor IPv6 multicast socket could be opened")
var errSendFailed = errors.New("mdnssd: query could not be sent on any interface/address family")

// udpTransport is the real Transport: per-interface IPv4+IPv6 multicast
// sockets on port 5353. This mirrors the wire-level mechanics dnssd uses
// internally for its own connection — but that code is unexported and
// unreusable there (see doc.go for why mdnssd exists), so this is a fresh,
// standalone implementation.
//
// IPv4 and IPv6 are treated as independently optional throughout: a
// container's default network commonly has a real IPv4 route but no usable
// IPv6 one (the socket opens fine — nothing about opening it requires a
// route — but every send then fails with EADDRNOTAVAIL). Requiring both
// families to work would silence IPv4 discovery entirely over one
// unrelated IPv6 misconfiguration, so failures are only fatal when NEITHER
// family works at all.
type udpTransport struct {
	pc4 *ipv4.PacketConn // nil if IPv4 is unavailable
	pc6 *ipv6.PacketConn // nil if IPv6 is unavailable

	ifaces []net.Interface
}

// NewUDPTransport opens IPv4 and IPv6 multicast sockets on port 5353 and
// joins the mDNS multicast group (224.0.0.251 / ff02::fb) on ifaceNames, or
// every multicast-capable "up" interface if ifaceNames is empty. Failing to
// open one address family is not fatal — only failing both is (see the
// udpTransport doc comment).
func NewUDPTransport(ifaceNames []string) (Transport, error) {
	ifaces, err := resolveMulticastIfaces(ifaceNames)
	if err != nil {
		return nil, err
	}
	if len(ifaces) == 0 {
		return nil, errNoMulticastIfaces
	}

	// Bind directly to the multicast group address (not 0.0.0.0), matching
	// dnssd's own approach: this is what lets other mDNS-using programs on
	// the same host (the OS's own resolver, avahi, dnssd's responder, ...)
	// share port 5353 concurrently.
	var pc4 *ipv4.PacketConn
	if conn4, err := net.ListenUDP("udp4", addrIPv4); err == nil {
		pc4 = ipv4.NewPacketConn(conn4)
		_ = pc4.SetControlMessage(ipv4.FlagInterface, true)
		_ = pc4.SetMulticastLoopback(true)
		_ = pc4.SetTTL(255) // RFC 6762 §11
		_ = pc4.SetMulticastTTL(255)
	}

	var pc6 *ipv6.PacketConn
	if conn6, err := net.ListenUDP("udp6", addrIPv6); err == nil {
		pc6 = ipv6.NewPacketConn(conn6)
		_ = pc6.SetControlMessage(ipv6.FlagInterface, true)
		_ = pc6.SetMulticastLoopback(true)
		_ = pc6.SetHopLimit(255) // RFC 6762 §11
		_ = pc6.SetMulticastHopLimit(255)
	}

	if pc4 == nil && pc6 == nil {
		return nil, errNoUsableSocket
	}

	for _, ifi := range ifaces {
		// A given interface may only support one address family (e.g.
		// IPv6-only, or a v4-only Docker bridge) — that's not fatal, just
		// means this interface won't carry that family's traffic.
		if pc4 != nil {
			_ = pc4.JoinGroup(&ifi, addrIPv4)
		}
		if pc6 != nil {
			_ = pc6.JoinGroup(&ifi, addrIPv6)
		}
	}

	return &udpTransport{pc4: pc4, pc6: pc6, ifaces: ifaces}, nil
}

// SendQuery is best-effort across interfaces and address families: it
// returns an error only if the message could not be sent anywhere at all.
// A working IPv4 path must not be taken down by an unrelated IPv6 send
// failure (or vice versa) — see the udpTransport doc comment.
func (t *udpTransport) SendQuery(ifaceName string, msg *dns.Msg) error {
	packed, err := msg.Pack()
	if err != nil {
		return err
	}

	sent := false
	for _, ifi := range t.ifaces {
		if ifaceName != "" && ifi.Name != ifaceName {
			continue
		}
		if t.pc4 != nil {
			if err := t.pc4.SetMulticastInterface(&ifi); err == nil {
				if _, err := t.pc4.WriteTo(packed, nil, addrIPv4); err == nil {
					sent = true
				}
			}
		}
		if t.pc6 != nil {
			if err := t.pc6.SetMulticastInterface(&ifi); err == nil {
				if _, err := t.pc6.WriteTo(packed, nil, addrIPv6); err == nil {
					sent = true
				}
			}
		}
	}
	if !sent {
		return errSendFailed
	}
	return nil
}

func (t *udpTransport) Read(ctx context.Context) <-chan InboundMessage {
	out := make(chan InboundMessage, 32)
	if t.pc4 != nil {
		go t.readLoop4(ctx, out)
	}
	if t.pc6 != nil {
		go t.readLoop6(ctx, out)
	}
	go func() {
		<-ctx.Done()
		_ = t.Close()
	}()
	return out
}

func (t *udpTransport) readLoop4(ctx context.Context, out chan<- InboundMessage) {
	buf := make([]byte, 65536)
	for {
		n, cm, _, err := t.pc4.ReadFrom(buf)
		if err != nil {
			return // closed (via ctx.Done, or Close()) or fatal read error
		}
		ifaceName := ""
		if cm != nil {
			ifaceName = ifaceNameFromIndex(cm.IfIndex)
		}
		m := new(dns.Msg)
		if err := m.Unpack(buf[:n]); err != nil {
			continue
		}
		select {
		case out <- InboundMessage{Msg: m, IfaceName: ifaceName}:
		case <-ctx.Done():
			return
		}
	}
}

func (t *udpTransport) readLoop6(ctx context.Context, out chan<- InboundMessage) {
	buf := make([]byte, 65536)
	for {
		n, cm, _, err := t.pc6.ReadFrom(buf)
		if err != nil {
			return
		}
		ifaceName := ""
		if cm != nil {
			ifaceName = ifaceNameFromIndex(cm.IfIndex)
		}
		m := new(dns.Msg)
		if err := m.Unpack(buf[:n]); err != nil {
			continue
		}
		select {
		case out <- InboundMessage{Msg: m, IfaceName: ifaceName}:
		case <-ctx.Done():
			return
		}
	}
}

func (t *udpTransport) Close() error {
	var firstErr error
	if t.pc4 != nil {
		if err := t.pc4.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if t.pc6 != nil {
		if err := t.pc6.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func ifaceNameFromIndex(idx int) string {
	ifi, err := net.InterfaceByIndex(idx)
	if err != nil {
		return ""
	}
	return ifi.Name
}

func resolveMulticastIfaces(names []string) ([]net.Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var want map[string]bool
	if len(names) > 0 {
		want = make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
	}

	var out []net.Interface
	for _, ifi := range all {
		if !isMulticastCapable(ifi) {
			continue
		}
		if want != nil && !want[ifi.Name] {
			continue
		}
		out = append(out, ifi)
	}
	return out, nil
}

func isMulticastCapable(ifi net.Interface) bool {
	return ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0
}
