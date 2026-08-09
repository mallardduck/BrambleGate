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

// udpTransport is the real Transport: per-interface IPv4+IPv6 multicast
// sockets on port 5353. This mirrors the wire-level mechanics dnssd uses
// internally for its own connection — but that code is unexported and
// unreusable there (see doc.go for why mdnssd exists), so this is a fresh,
// standalone implementation.
type udpTransport struct {
	pc4 *ipv4.PacketConn
	pc6 *ipv6.PacketConn

	ifaces []net.Interface
}

// NewUDPTransport opens IPv4 and IPv6 multicast sockets on port 5353 and
// joins the mDNS multicast group (224.0.0.251 / ff02::fb) on ifaceNames, or
// every multicast-capable "up" interface if ifaceNames is empty.
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
	conn4, err := net.ListenUDP("udp4", addrIPv4)
	if err != nil {
		return nil, err
	}
	pc4 := ipv4.NewPacketConn(conn4)
	_ = pc4.SetControlMessage(ipv4.FlagInterface, true)
	_ = pc4.SetMulticastLoopback(true)
	_ = pc4.SetTTL(255) // RFC 6762 §11
	_ = pc4.SetMulticastTTL(255)

	conn6, err := net.ListenUDP("udp6", addrIPv6)
	if err != nil {
		_ = conn4.Close()
		return nil, err
	}
	pc6 := ipv6.NewPacketConn(conn6)
	_ = pc6.SetControlMessage(ipv6.FlagInterface, true)
	_ = pc6.SetMulticastLoopback(true)
	_ = pc6.SetHopLimit(255) // RFC 6762 §11
	_ = pc6.SetMulticastHopLimit(255)

	for _, ifi := range ifaces {
		// A given interface may only support one address family (e.g.
		// IPv6-only, or a v4-only Docker bridge) — that's not fatal, just
		// means this interface won't carry that family's traffic.
		_ = pc4.JoinGroup(&ifi, addrIPv4)
		_ = pc6.JoinGroup(&ifi, addrIPv6)
	}

	return &udpTransport{pc4: pc4, pc6: pc6, ifaces: ifaces}, nil
}

func (t *udpTransport) SendQuery(ifaceName string, msg *dns.Msg) error {
	packed, err := msg.Pack()
	if err != nil {
		return err
	}

	var firstErr error
	report := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, ifi := range t.ifaces {
		if ifaceName != "" && ifi.Name != ifaceName {
			continue
		}
		if err := t.pc4.SetMulticastInterface(&ifi); err == nil {
			_, err := t.pc4.WriteTo(packed, nil, addrIPv4)
			report(err)
		}
		if err := t.pc6.SetMulticastInterface(&ifi); err == nil {
			_, err := t.pc6.WriteTo(packed, nil, addrIPv6)
			report(err)
		}
	}
	return firstErr
}

func (t *udpTransport) Read(ctx context.Context) <-chan InboundMessage {
	out := make(chan InboundMessage, 32)
	go t.readLoop4(ctx, out)
	go t.readLoop6(ctx, out)
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
	err4 := t.pc4.Close()
	err6 := t.pc6.Close()
	if err4 != nil {
		return err4
	}
	return err6
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
