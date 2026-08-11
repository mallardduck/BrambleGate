package vlancache

import (
	"strconv"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/mallardduck/BrambleGate/pluginreg"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

func init() {
	plugin.Register("vlancache", setup)
	pluginreg.Register(pluginreg.Descriptor{
		Name: "vlancache",
		Kind: pluginreg.CoreDNSPlugin,
	})
}

// setup parses the vlancache stanza and inserts the handler into the chain.
//
// Corefile syntax:
//
//	vlancache {
//	    capacity 10000
//	    servfail 5s
//	}
//
// Bare "vlancache" uses the plugin's own defaults for both. vlans is read
// from vlanmatch.Current() at parse time, matching localrecords' own
// no-runtime-refresh convention — a config change requires an engine.Reload,
// which rebuilds this plugin from scratch anyway.
func setup(c *caddy.Controller) error {
	vc, err := parse(c)
	if err != nil {
		pluginreg.SetLoaded("vlancache", false, "failed to start: "+err.Error())
		return plugin.Error("vlancache", err)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		vc.Next = next
		return vc
	})
	pluginreg.SetLoaded("vlancache", true, "")
	return nil
}

func parse(c *caddy.Controller) (*VlanCache, error) {
	vc := &VlanCache{vlans: vlanmatch.Current(), failTTL: defaultFailTTL}
	capacity := defaultCap
	seen := false

	for c.Next() { // "vlancache"
		if seen {
			return nil, c.Err("vlancache: multiple stanzas in one server block are not supported")
		}
		seen = true

		for c.NextBlock() {
			switch strings.ToLower(c.Val()) {
			case "capacity":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				n, err := strconv.Atoi(c.Val())
				if err != nil || n <= 0 {
					return nil, c.Errf("vlancache: invalid capacity %q", c.Val())
				}
				capacity = n
			case "servfail":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				d, err := time.ParseDuration(c.Val())
				if err != nil {
					return nil, err
				}
				if d < 0 || d > maxFailTTL {
					return nil, c.Err("vlancache: servfail ttl must be between 0 and 5m (RFC 2308)")
				}
				vc.failTTL = d
			default:
				return nil, c.Errf("vlancache: unknown property %q", c.Val())
			}
		}
	}
	vc.store = newStore(capacity)
	return vc, nil
}
