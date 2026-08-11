package querylog

import (
	"strconv"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/mallardduck/BrambleGate/pluginreg"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// defaultCapacity is the ring's entry count when no "capacity" sub-directive
// is given.
const defaultCapacity = 4096

func init() {
	plugin.Register("querylog", setup)
	pluginreg.Register(pluginreg.Descriptor{
		Name: "querylog",
		Kind: pluginreg.CoreDNSPlugin,
	})
}

// setup parses the querylog stanza and inserts the handler into the chain.
//
// Corefile syntax (rendered by configgen):
//
//	querylog {
//	    capacity 4096
//	}
//
// Both the stanza and the capacity sub-directive are optional; a bare
// "querylog" uses defaultCapacity.
func setup(c *caddy.Controller) error {
	capacity := defaultCapacity

	for c.Next() {
		if len(c.RemainingArgs()) != 0 {
			return setupErr(c.ArgErr())
		}
		for c.NextBlock() {
			switch strings.ToLower(c.Val()) {
			case "capacity":
				if !c.NextArg() {
					return setupErr(c.ArgErr())
				}
				n, err := strconv.Atoi(c.Val())
				if err != nil || n <= 0 {
					return setupErr(c.Errf("querylog: invalid capacity %q", c.Val()))
				}
				capacity = n
			default:
				return setupErr(c.Errf("unknown property %q", c.Val()))
			}
		}
	}

	ring := NewRing(capacity)
	SetCurrent(ring)

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &QueryLog{Next: next, Ring: ring, VLANs: vlanmatch.Current()}
	})
	pluginreg.SetLoaded("querylog", true, "")
	return nil
}

func setupErr(err error) error {
	pluginreg.SetLoaded("querylog", false, "failed to start: "+err.Error())
	return plugin.Error("querylog", err)
}
