package engine

// corefileInput adapts in-memory Corefile bytes to caddy.Input. The engine only
// ever receives rendered bytes (from configgen), never a path on disk, so Path
// is a stable label used purely for CoreDNS log/error messages.
//
// caddy.Input is: Body() []byte, Path() string, ServerType() string — verified
// against github.com/coredns/caddy v1.1.4-0.20250930002214-15135a999495.
type corefileInput struct {
	body []byte
}

func (c corefileInput) Body() []byte { return c.body }

func (c corefileInput) Path() string { return "Corefile" }

// ServerType is "dns" so caddy dispatches to the CoreDNS dns server type
// (registered by the blank import of core/dnsserver in directives.go).
func (c corefileInput) ServerType() string { return "dns" }
