// Package vlancfg converts model's persisted VLAN declarations into
// vlanmatch's matching shape — the one canonical translation between the
// persistence context (model/store) and the matching context (vlanmatch),
// used by internal/cli and internal/gui/service.go wherever settings.VLANs
// needs to become a vlanmatch.Table (dev-docs/query-log.md). Lives under
// internal/ rather than as its own module: it's shared code, not a shared
// runtime instance, and both its callers already live in the root module —
// there's no cross-module boundary here to justify a sibling module the way
// vlanmatch itself needed one.
package vlancfg

import (
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/vlanmatch"
)

// Build converts settings' declared VLANs into vlanmatch's own minimal
// shape. vlanmatch deliberately doesn't import model (so localrecords/
// mdnsbridge, which avoid model, can still depend on vlanmatch) — this is
// the one place that conversion happens for the composition root and the
// GUI service, instead of each defining its own copy.
func Build(vlans []model.VLAN) []vlanmatch.VLAN {
	out := make([]vlanmatch.VLAN, len(vlans))
	for i, v := range vlans {
		out[i] = vlanmatch.VLAN{Name: v.Name, CIDRs: v.CIDRs}
	}
	return out
}
