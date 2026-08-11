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

// ringMarker is the caddy Instance-storage key (via Controller.Get/Set) that
// records "the ring decision for this parse has already been made" — see the
// comment in setup where it's used.
type ringMarker struct{}

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

	// Ring lifecycle: shared process-wide, and deliberately NOT reset by every
	// reload — from the operator's point of view BrambleGate itself didn't
	// restart just because they applied a settings change, so their query
	// history shouldn't vanish either, even though CoreDNS tears down and
	// rebuilds its Instance underneath.
	//
	// Multi-listener deployments (Plain + DoT/DoH/DoQ/DoH3) render a querylog
	// directive into every server block (buildServerBlock in configgen), and
	// CoreDNS calls setup() once per block — all within the same reload. Every
	// block must land on the exact same Ring, or each listener's handler ends
	// up with its own independent ring and Current() (what the GUI reads)
	// only ever reflects whichever block was parsed last.
	//
	// c.Get/c.Set (backed by the caddy Instance's own storage, shared by every
	// Controller/block of one parse but freshly empty on the next) gives an
	// explicit, reload-scoped "have I already decided the ring for this
	// parse" signal — unlike inferring it from capacity equality, which can't
	// tell "another block, same reload" apart from "a genuine reload that
	// happens to reuse the same capacity" without accidentally resetting
	// history on every unrelated reload.
	if c.Get(ringMarker{}) == nil {
		if current := Current(); current == nil || current.Cap() != capacity {
			SetCurrent(NewRing(capacity))
		}
		c.Set(ringMarker{}, true)
	}
	ring := Current()

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
