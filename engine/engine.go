// Package engine wraps the CoreDNS server instance via github.com/coredns/caddy,
// driving the same primitive coremain.Run uses (caddy.Start / Instance.Restart /
// Instance.Stop) as ordinary Go calls instead of through a CLI + OS-signal shell.
// See docs/dns-engine.md.
package engine

import (
	"sync"

	"github.com/coredns/caddy"
)

// Engine owns a running CoreDNS instance and mediates graceful, in-process
// config swaps. It is safe for concurrent use: the GUI goroutine may call Reload
// while the shutdown path calls Stop.
type Engine struct {
	mu       sync.Mutex
	instance *caddy.Instance
}

// New starts CoreDNS with the given rendered Corefile content.
func New(corefile []byte) (*Engine, error) {
	inst, err := caddy.Start(corefileInput{body: corefile})
	if err != nil {
		return nil, err
	}
	return &Engine{instance: inst}, nil
}

// Reload performs a graceful, in-process config swap. On failure the previous
// config keeps serving and the error is returned to the caller — surface it to
// the GUI user rather than silently dropping their edit (see docs/architecture.md).
func (e *Engine) Reload(corefile []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	newInst, err := e.instance.Restart(corefileInput{body: corefile})
	if err != nil {
		return err
	}
	e.instance = newInst
	return nil
}

// Stop gracefully shuts down the running instance. Call it once from the shared
// shutdown context in main().
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.instance.Stop()
}

// Wait blocks until the running instance is stopped (by Stop or an internal
// fatal error), mirroring caddy.Instance.Wait. main() uses it to keep the engine
// goroutine alive.
func (e *Engine) Wait() {
	e.mu.Lock()
	inst := e.instance
	e.mu.Unlock()
	inst.Wait()
}
