package mdnssd

import (
	"context"
	"sync"

	"github.com/miekg/dns"
)

// fakeTransport is a Transport test double: SendQuery records messages
// in-memory instead of touching a socket, and deliver injects inbound
// messages as if received over the network.
type fakeTransport struct {
	mu      sync.Mutex
	sent    []*dns.Msg
	inbound chan InboundMessage
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{inbound: make(chan InboundMessage, 32)}
}

func (f *fakeTransport) SendQuery(ifaceName string, msg *dns.Msg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeTransport) Read(ctx context.Context) <-chan InboundMessage { return f.inbound }

func (f *fakeTransport) Close() error { return nil }

// deliver injects msg as if it arrived on ifaceName.
func (f *fakeTransport) deliver(msg *dns.Msg, ifaceName string) {
	f.inbound <- InboundMessage{Msg: msg, IfaceName: ifaceName}
}

func (f *fakeTransport) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeTransport) sentMessages() []*dns.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*dns.Msg, len(f.sent))
	copy(out, f.sent)
	return out
}
