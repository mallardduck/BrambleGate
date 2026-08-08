// Package engine wraps the CoreDNS server instance via github.com/coredns/caddy,
// driving the same primitive coremain.Run uses (caddy.Start / Instance.Restart /
// Instance.Stop) as ordinary Go calls instead of through a CLI + OS-signal shell.
// See docs/dns-engine.md.
package engine

import (
	"fmt"
	"runtime"
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
//
// On Windows this guarantee doesn't hold — see reloadStopStart.
func (e *Engine) Reload(corefile []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if runtime.GOOS == "windows" {
		return e.reloadStopStart(corefile)
	}
	newInst, err := e.instance.Restart(corefileInput{body: corefile})
	if err != nil {
		return err
	}
	e.instance = newInst
	return nil
}

// reloadStopStart is the Windows fallback for Reload. Instance.Restart's
// graceful listener handoff works by duplicating each listening socket via
// (*net.TCPListener).File() and handing the duplicate to the new instance —
// this is how the previous config keeps serving even if the new one fails to
// start. Go's Windows implementation cannot do that duplication for a socket
// already in the listening state; it fails every time with a generic "device
// attached to the system is not functioning" error. This is a Windows-only
// limitation of the underlying net/caddy mechanism, confirmed by the fact that
// the identical reload succeeds cleanly in the real deployment target (a Linux
// container) — not a bug in this codebase.
//
// The fallback: stop the old instance, then start a new one on the same
// ports. Unlike Restart, this briefly stops serving DNS, and — if the new
// config fails to start — leaves nothing listening at all (the "previous
// config keeps serving on failure" guarantee does not hold here). That
// trade-off is acceptable only because this path is Windows-only, i.e. never
// hit in production (Docker/Linux), where Restart's graceful handoff is used
// and works.
func (e *Engine) reloadStopStart(corefile []byte) error {
	if err := e.instance.Stop(); err != nil {
		return fmt.Errorf("stop previous instance: %w", err)
	}
	newInst, err := caddy.Start(corefileInput{body: corefile})
	if err != nil {
		return fmt.Errorf("start new instance (previous instance already stopped — DNS is down until this is fixed): %w", err)
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
