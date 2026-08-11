// Package vlancache is a CoreDNS cache plugin that replaces the stock cache
// plugin for BrambleGate's ECS-enabled deployments. The stock cache plugin
// keys purely on qname/qtype/qclass, with no subnet dimension, so it can't
// safely run alongside EDNS0 Client Subnet: an upstream that varies its
// answer by client (e.g. per-VLAN blocklist groups) would have one client's
// cached answer served to every other client.
//
// vlancache fixes this by keying cache entries on the requester's VLAN (via
// vlanmatch, the same primitive plugins/localrecords uses for split-horizon
// answers) by default, and by an upstream-echoed RFC 7871 SCOPE
// PREFIX-LENGTH when one is available — a progressive enhancement layered
// over the VLAN default, not a replacement for it. See dev-docs' vlancache
// design note.
package vlancache
