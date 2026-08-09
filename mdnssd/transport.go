package mdnssd

import (
	"context"

	"github.com/miekg/dns"
)

// InboundMessage is one mDNS message received on a network interface.
type InboundMessage struct {
	Msg       *dns.Msg
	IfaceName string
}

// Transport sends and receives raw mDNS messages. transport_udp.go provides
// the real per-interface multicast implementation; tests use a fake.
type Transport interface {
	// SendQuery multicasts msg. ifaceName selects one bound interface, or ""
	// for all of them.
	SendQuery(ifaceName string, msg *dns.Msg) error
	// Read returns a channel of inbound messages. Implementations should
	// stop producing once ctx is done.
	Read(ctx context.Context) <-chan InboundMessage
	Close() error
}
