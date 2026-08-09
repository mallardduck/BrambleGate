package mdnsbridge

import (
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/mallardduck/BrambleGate/pluginreg"
)

func init() {
	plugin.Register("mdnsbridge", setup)
	pluginreg.Register(pluginreg.Descriptor{
		Name: "mdnsbridge",
		Kind: pluginreg.CoreDNSPlugin,
		// mdnsbridge runs ahead of localrecords in the chain and falls
		// through to it on a miss (docs/plugins.md) — advisory, not
		// enforced here; the real chain order is engine/directives.go.
		DependsOn: []string{"localrecords"},
	})
}

// The discovery Table is created and owned by the host process (cli) and injected
// here before the engine starts, because it must outlive engine reloads and be
// shared with the GUI. setup grabs the injected Table so the plugin can read it.
var (
	injectMu    sync.RWMutex
	injectedTbl *Table
)

// SetTable injects the process-owned discovery table. Call it before engine.New
// (and before any reload) when mDNS is enabled.
func SetTable(t *Table) {
	injectMu.Lock()
	defer injectMu.Unlock()
	injectedTbl = t
}

func sharedTable() *Table {
	injectMu.RLock()
	defer injectMu.RUnlock()
	return injectedTbl
}

// setup parses the (argument-free) mdnsbridge stanza and wires the handler to the
// injected Table.
//
//	mdnsbridge
func setup(c *caddy.Controller) error {
	for c.Next() {
		if len(c.RemainingArgs()) != 0 {
			err := c.ArgErr()
			pluginreg.SetLoaded("mdnsbridge", false, "failed to start: "+err.Error())
			return plugin.Error("mdnsbridge", err)
		}
		for c.NextBlock() {
			err := c.Errf("unknown property %q", c.Val())
			pluginreg.SetLoaded("mdnsbridge", false, "failed to start: "+err.Error())
			return plugin.Error("mdnsbridge", err)
		}
	}

	tbl := sharedTable()
	if tbl == nil {
		pluginreg.SetLoaded("mdnsbridge", false, "failed to start: "+errNoTable.Error())
		return plugin.Error("mdnsbridge", errNoTable)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &MDNSBridge{Next: next, Table: tbl}
	})
	pluginreg.SetLoaded("mdnsbridge", true, "")
	return nil
}

type mdnsError string

func (e mdnsError) Error() string { return string(e) }

const errNoTable = mdnsError("no discovery table injected (SetTable must be called before engine start)")
