package cli

import (
	"fmt"
	"strings"
)

// forwardTarget renders the upstream address for the forward plugin, honoring an
// encrypted internal hop when upstream_dns.protocol is dot.
func (s Settings) forwardTarget() string {
	if s.UpstreamDNS.Protocol == "dot" {
		return "tls://" + s.UpstreamDNS.Address
	}
	return s.UpstreamDNS.Address
}

// renderCorefile builds the Phase 1 forward-only Corefile: one server block per
// enabled listener, each forwarding everything to the upstream ad-block resolver.
// No localrecords/mdnsbridge yet (Phase 2+). certFile/keyFile are only referenced
// when the DoT listener is enabled.
//
// This is a hand-rolled Phase 1 stand-in; Phase 2 replaces it with configgen.
func renderCorefile(s Settings, certFile, keyFile string) []byte {
	var b strings.Builder
	fwd := s.forwardTarget()

	block := func(addr string, tls bool) {
		b.WriteString(addr + " {\n")
		if tls {
			fmt.Fprintf(&b, "\ttls %s %s\n", certFile, keyFile)
		}
		fmt.Fprintf(&b, "\tforward . %s\n", fwd)
		b.WriteString("\tcache\n")
		b.WriteString("\terrors\n")
		b.WriteString("\tlog\n")
		b.WriteString("}\n")
	}

	if s.Listeners.Plain.Enabled {
		block(fmt.Sprintf(".:%d", s.Listeners.Plain.Port), false)
	}
	if s.Listeners.DoT.Enabled {
		block(fmt.Sprintf("tls://.:%d", s.Listeners.DoT.Port), true)
	}
	return []byte(b.String())
}
