package mdnsbridge

import (
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

func init() { plugin.Register("mdnsbridge", setup) }

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
			return plugin.Error("mdnsbridge", c.ArgErr())
		}
		for c.NextBlock() {
			return plugin.Error("mdnsbridge", c.Errf("unknown property %q", c.Val()))
		}
	}

	tbl := sharedTable()
	if tbl == nil {
		return plugin.Error("mdnsbridge", errNoTable)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &MDNSBridge{Next: next, Table: tbl}
	})
	return nil
}

type mdnsError string

func (e mdnsError) Error() string { return string(e) }

const errNoTable = mdnsError("no discovery table injected (SetTable must be called before engine start)")
