package querylog

import "context"

type contextKey struct{}

// NewContext returns a copy of ctx carrying e. The handler stashes a mutable
// *Entry before calling next.ServeDNS; downstream plugins that self-attribute
// (localrecords, mdnsbridge, later a hypothetical blocklist plugin) fetch it
// via FromContext and mutate it in place — the handler reads the same pointer
// back after next.ServeDNS returns (docs/query-log.md).
func NewContext(ctx context.Context, e *Entry) context.Context {
	return context.WithValue(ctx, contextKey{}, e)
}

// FromContext returns the *Entry stashed by NewContext, or nil if none was
// stashed (or a nil *Entry was stashed) — nil-safe so a downstream plugin can
// call this unconditionally even when querylog isn't in the chain, e.g. in
// its own unit tests that don't wrap ctx with NewContext.
func FromContext(ctx context.Context) *Entry {
	e, _ := ctx.Value(contextKey{}).(*Entry)
	return e
}
