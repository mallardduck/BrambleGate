// Package engine wraps the CoreDNS server instance (via github.com/coredns/caddy)
// without going through coremain.Run, exposing New/Reload/Stop for in-process
// lifecycle control. See docs/dns-engine.md.
package engine
