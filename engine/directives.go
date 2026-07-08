package engine

import (
	// Registers the "dns" ServerType so caddy.Start can dispatch our
	// corefileInput. coremain does this implicitly; we do it explicitly.
	_ "github.com/coredns/coredns/core/dnsserver"

	// Compiled-in plugins. Blank-importing registers each plugin's setup under
	// its directive name; a directive listed in Directives below but not imported
	// here simply can't be used in a Corefile (it isn't an error until referenced).
	_ "github.com/coredns/coredns/plugin/bind"
	_ "github.com/coredns/coredns/plugin/cache"
	_ "github.com/coredns/coredns/plugin/errors"
	_ "github.com/coredns/coredns/plugin/forward"
	_ "github.com/coredns/coredns/plugin/log"
	_ "github.com/coredns/coredns/plugin/tls"
	_ "github.com/coredns/coredns/plugin/whoami"

	// BrambleDNS custom plugins. Blank-importing registers them under their
	// directive names (reserved in Directives below). mdnsbridge joins in Phase 5.
	_ "github.com/mallardduck/BrambleDNS/plugins/localrecords"

	"github.com/coredns/coredns/core/dnsserver"
)

// init declares which compiled-in plugins exist and, crucially, the order they
// run in for every request. This is independent of any runtime Corefile content
// (see docs/dns-engine.md). Importing core/dnsserver already sets a default
// Directives list; we override it to slot the two custom BrambleDNS plugins in
// ahead of forward, so static records (localrecords) and mDNS-discovered names
// (mdnsbridge) are answered before anything falls through to the upstream
// ad-block resolver (see docs/plugins.md).
//
// The list is CoreDNS's canonical order (core/dnsserver/zdirectives.go) with
// "localrecords" and "mdnsbridge" inserted immediately before "forward".
func init() {
	dnsserver.Directives = []string{
		"root",
		"metadata",
		"geoip",
		"cancel",
		"tls",
		"proxyproto",
		"quic",
		"grpc_server",
		"https",
		"https3",
		"timeouts",
		"multisocket",
		"reload",
		"nsid",
		"bufsize",
		"bind",
		"debug",
		"trace",
		"ready",
		"health",
		"pprof",
		"prometheus",
		"errors",
		"log",
		"dnstap",
		"local",
		"dns64",
		"any",
		"chaos",
		"loadbalance",
		"tsig",
		// BrambleDNS custom plugins run BEFORE cache: localrecords answers are
		// per-VLAN (split-horizon) and must never be globally cached — the cache
		// is not keyed by client subnet, so caching them would serve one VLAN's
		// answer to another. They also don't need caching (in-memory, instant).
		// Out-of-zone queries fall through past cache to forward as usual.
		"localrecords",
		"mdnsbridge",
		"cache",
		"rewrite",
		"acl",
		"header",
		"dnssec",
		"autopath",
		"minimal",
		"template",
		"transfer",
		"hosts",
		"route53",
		"azure",
		"clouddns",
		"k8s_external",
		"kubernetes",
		"file",
		"auto",
		"secondary",
		"etcd",
		"loop",
		"forward",
		"grpc",
		"erratic",
		"whoami",
		"on",
		"sign",
		"view",
		"nomad",
	}
}
