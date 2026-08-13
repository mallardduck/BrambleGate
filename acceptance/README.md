# BrambleGate DNS Acceptance Suite

Scripts the real-network validation checks in
[`../dev-docs/testing-guide.md`](../dev-docs/testing-guide.md) (Phase 10 of
[`../dev-docs/roadmap.md`](../dev-docs/roadmap.md)) so re-running them after a
major change is a command, not a re-derivation of `dig`/`openssl` invocations
by hand.

**Status: scaffold only.** The CLI, config loading, and check registry are
wired end-to-end; every individual check currently returns `TODO` (see
`checks/protocol/*.go` and `checks/bramblegate/*.go`) rather than a real
result. Fill these in incrementally — the architecture doesn't change as they
go from stub to real.

## Why its own module

Per `dev-docs/repo-layout.md`'s "Why separate modules" sharing test: this
tool never imports BrambleGate's internal packages, only talks to an
already-running instance over the network (DNS queries, TLS handshakes,
`/api/*` HTTP calls). It's meant to run from any machine with network reach
to BrambleGate — including a laptop on a specific VLAN, which split-horizon
checks require — not just the BrambleGate host itself.

## Two axes: Scope and Tier

Every check is classified along two independent axes (`checks/check.go`):

- **Scope** is *what* the check validates.
  - `protocol` (`checks/protocol/`) — DNS-standards conformance: correct
    regardless of what BrambleGate specifically has configured, and in
    principle runnable against any spec-compliant DNS/DoT/DoH server (TLS
    chain validity, NXDOMAIN/AA-bit semantics, TCP fallback). These only need
    a target address and a domain to probe.
  - `bramblegate` (`checks/bramblegate/`) — does *this* BrambleGate instance
    behave the way its own config says it should: a VLAN's split-horizon
    override, a `hosts.yaml` entry, `/api/clients`/`/api/mdns` state, the
    forward-path leak check. Meaningless without the config that defines the
    expected outcome.
- **Tier** is *what the check needs to run*: `network` (reachable from
  anywhere), `local` (must run from a device physically on the VLAN under
  test — split-horizon's source IP selects the override), `mobile`
  (deferred, needs a connected Android device via ADB — off by default via
  `mobile.enabled: false`; candidate client libraries are
  `github.com/prife/goadb` or `github.com/taigrr/adb`, not yet a dependency).

What's **not** here, and stays a manual checklist in `testing-guide.md`:
physically power-cycling a device, browsing mDNS self-advertisement from a
second machine, anything that needs a human looking at a phone's UI.

## Usage

```sh
cp acceptance.example.yaml acceptance.yaml   # fill in your real network
go run . list                                 # see what would run, and its scope/tier
go run . list --online                        # same, but with real discovered counts
go run . run                                  # run everything (always needs a live target)
go run . run --scope protocol                 # just DNS-standards conformance
go run . run --scope bramblegate --tier network
```

Output is a markdown table shaped like `testing-guide.md`'s results-log
tables, so a run's output can be pasted straight into that doc.

## Config vs. discovery

`acceptance.yaml` only declares what genuinely can't be derived from a
running instance: `target` (how to reach it), `domain` (the zone under
test), and true test *expectations* — a VLAN's expected split-horizon
answer, an external `upstream.test_domain` to compare against. Everything
else that used to be hand-duplicated config (VLAN names, the upstream
resolver address, `hosts.yaml` entries, mdns-linked `records.yaml` entries)
is instead fetched live from the instance's own read-only `/api/*` at
startup (`discover/discover.go`) — see `GET /api/settings`, `GET
/api/hosts`, `GET /api/records`.

`run` always fetches discovery — it needs it to build real checks, and
fails fast if `target.api_base` is unreachable. `list` is the offline-first
dry-run: by default it shows one placeholder line per discovery-scaled
category (`hosts/*`, `mdns/*`) without hitting the network; pass `--online`
to fetch for real and see the exact concrete check list instead.

A declared `vlans[].name` that doesn't match any VLAN discovery finds on
`/api/settings` fails as config drift, rather than silently querying a VLAN
name the server doesn't actually have.

## Adding a check

1. Implement `checks.Check` (`Name`/`Tier`/`Scope`/`Run`) — in
   `checks/protocol/` if it's true of any spec-compliant DNS server, or
   `checks/bramblegate/` if it depends on this instance's configured content.
2. Register it in `registry.go`'s `Registry` — one entry per config-driven
   instance (e.g. one `SplitHorizon` per `cfg.VLANs` entry) if it scales
   with hand-declared config, one entry per `cfg.Discovered` item (e.g.
   `Hosts` per `cfg.Discovered.Hosts` entry) if it scales with live server
   state — pair it with a placeholder `Check` (see `HostsDiscovery`,
   `MDNSDiscovery`) so `list` without `--online` still shows the category —
   or a single static entry otherwise.
