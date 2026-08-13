//go:build !linux

package gatewaydetect

import "net"

// defaultGateways is a no-op on non-Linux platforms for now — DetectLive
// then falls straight through to the "network + 1" heuristic for every
// VLAN (dev-docs/client-names.md). Real per-OS routing-table readers
// (Windows: GetIpForwardTable2 via golang.org/x/sys/windows; macOS:
// route/sysctl) are meant to be added here incrementally, one build-tagged
// file per OS, without touching the Linux implementation or detect()'s
// pure core.
func defaultGateways() map[string]net.IP { return nil }
