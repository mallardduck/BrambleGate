// Package checks defines the Check interface every acceptance check
// implements, and the shared Result/Status/Tier/Scope vocabulary the runner
// and reporter use.
//
// Two axes classify every check, deliberately orthogonal:
//
//   - Tier is an execution requirement: what the check needs to run at all
//     (network reach, a specific VLAN, a connected Android device).
//   - Scope is what the check is actually validating: ScopeProtocol checks
//     are DNS-standards conformance — true of any spec-compliant DoT/DoH/DNS
//     server, not specific to what BrambleGate happens to have configured
//     (see checks/protocol). ScopeConfig checks are BrambleGate-specific:
//     meaningless without knowing what was configured to happen — a VLAN's
//     split-horizon override, a hosts.yaml entry, /api/clients state (see
//     checks/bramblegate). Conflating the two made the original flat
//     checks/ package's "acceptance suite" framing misleading: most of what
//     it validated was really "does BrambleGate do what its own config
//     says," not "is this a correct DNS server."
package checks

import (
	"context"

	"github.com/mallardduck/BrambleGate/acceptance/config"
)

// Tier groups checks by what they require to run.
type Tier string

const (
	// TierNetwork checks need only network reach to a running BrambleGate instance
	// (DNS queries, TLS handshakes, HTTP calls to /api/*) — runnable from any host.
	TierNetwork Tier = "network"
	// TierLocal checks are the same shape as TierNetwork but only meaningful when
	// run from a device physically on the VLAN under test (e.g. split-horizon
	// checks, where the source IP is what selects the override).
	TierLocal Tier = "local"
	// TierMobile checks require a connected Android device via adb. Optional,
	// off by default, skipped cleanly when config.Mobile.Enabled is false or the
	// adb binary isn't found. See mobile/adb.go.
	TierMobile Tier = "mobile"
)

// Scope classifies what a check is actually validating, independent of Tier.
type Scope string

const (
	// ScopeProtocol checks validate DNS-standards conformance: correct
	// regardless of BrambleGate's specific configured content, and in
	// principle runnable against any spec-compliant DNS/DoT/DoH server.
	// See checks/protocol.
	ScopeProtocol Scope = "protocol"
	// ScopeBrambleGate checks validate that this specific BrambleGate
	// instance behaves the way its own config says it should — split-horizon
	// overrides, hosts.yaml entries, /api/* state. Meaningless without the
	// config that defines the expected outcome. See checks/bramblegate.
	ScopeBrambleGate Scope = "bramblegate"
)

type Status string

const (
	Pass           Status = "PASS"
	Fail           Status = "FAIL"
	Skip           Status = "SKIP"
	NotImplemented Status = "TODO"
)

// Result is one check's outcome, shaped to drop straight into a
// testing-guide.md-style results-log table.
type Result struct {
	Check  string
	Tier   Tier
	Scope  Scope
	Status Status
	Detail string
}

// Check is one acceptance assertion — the scripted equivalent of one row in
// testing-guide.md's results-log tables.
type Check interface {
	// Name identifies the check in report output, e.g. "splithorizon/trusted".
	Name() string
	Tier() Tier
	Scope() Scope
	Run(ctx context.Context, cfg *config.Config) Result
}

// Todo builds a not-implemented Result — exported so subpackages
// (checks/protocol, checks/bramblegate) can use it without duplicating the
// shape.
func Todo(c Check, why string) Result {
	return Result{Check: c.Name(), Tier: c.Tier(), Scope: c.Scope(), Status: NotImplemented, Detail: why}
}
