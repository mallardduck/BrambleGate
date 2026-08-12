// Package hosts is BrambleGate's pluginreg wrapper around the stock CoreDNS
// hosts plugin (dev-docs/static-hosts.md) — not a CoreDNS-chain plugin
// itself. The actual DNS-serving code is entirely the stock
// github.com/coredns/coredns/plugin/hosts package, blank-imported directly
// in internal/engine/directives.go; this package exists only so "hosts" has
// a pluginreg.Descriptor to report against (dev-docs/plugin-system.md),
// same as every other BrambleGate component. Kind is CoreDNSPlugin (it does
// run in the directive chain), not BrambleOnly (contrast
// plugins/mdnsadvertise, which owns a real background component of its
// own).
//
// Part of the root module, not a sibling one (dev-docs/repo-layout.md's
// sharing test): only internal/engine (blank import) and internal/cli/
// internal/gui (SetLoaded, at each successful reload — hosts has no
// setup() of its own to call it from) ever reference "hosts", both inside
// the root module tree.
package hosts

import "github.com/mallardduck/BrambleGate/pluginreg"

func init() {
	pluginreg.Register(pluginreg.Descriptor{
		Name: "hosts",
		Kind: pluginreg.CoreDNSPlugin,
	})
}
