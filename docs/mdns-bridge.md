# mDNS bridge

Devices that only announce themselves via mDNS (`.local` names — printers,
Chromecasts, HomeKit accessories, ...) don't have real DNS records and don't
cross VLAN boundaries. The mDNS bridge discovers these announcements and can
turn them into normal `home.arpa` DNS answers, with a review step rather than
blind auto-publishing.

This is two independent features that share the `mdns` config block:
**discovery** (browsing for other devices) and **self-advertisement**
(BrambleGate announcing itself, the reverse direction).

## Discovery

```yaml
mdns:
  enabled: true
  interfaces: [all]     # [] and [all] both mean "all multicast interfaces"
  suffix: home.arpa      # default zone a discovered name maps into
  service_types: [default]   # curated common-types list (see below for alternatives)
  auto_publish: []        # e.g. [{ service: _airplay._tcp, vlan: trusted }]
```

Once enabled, discovered devices show up as candidates at `/mdns` in the GUI —
nothing is served as a DNS answer automatically unless it matches
`auto_publish`.

`service_types` has four distinct settings:

| Value | Behavior |
| --- | --- |
| *(empty)* | Browse nothing — no fixed list, no dynamic discovery. |
| `[default]` | Browse a curated list of common types (`_http._tcp`, `_airplay._tcp`, `_googlecast._tcp`, ...). The GUI settings form pre-fills this for you; only truly-blank means "nothing" there. |
| `[all]` | Discover whatever types are actually being advertised on the network via the DNS-SD meta-query, instead of only ever asking about a fixed list. Surfaces devices the curated default can't (IoT/vendor-specific types), at the cost of noisier `/mdns` candidates and a small amount of extra multicast traffic while types are enumerated. |
| an explicit list | Browse only those exact types. |

### Selectors — one primitive, three uses

A selector matches on a discovery's `service`, `instance`, `host`, `txt` map,
source `vlan`, and/or `family` — every field you set must match
(case-insensitive glob), and an unset field matches anything. A list of
selectors is OR'd together.

- **`auto_publish`** — a selector list; any discovery matching one is served
  live immediately, no GUI action needed.
- **`naming`** — `{match, suffix}` rules; the first matching selector picks
  which DNS suffix a discovery maps into (default: `mdns.suffix`).
- **promotion** (below) — stores a selector as the durable link between a DNS
  name and a discovery.

### Publish / unpublish / promote (GUI, `/mdns`)

- **Publish / unpublish** — serve or stop serving a candidate under its
  discovered name. Runtime-only: no `records.yaml` write, no reload, gone if
  the container restarts.
- **Promote** — the durable version. Writes a `type: mdns` record into
  `records.yaml` (through the normal save → validate → reload pipeline) with
  a `match` selector keyed on the device's host:

  ```yaml
  records:
    - name: printer.home.arpa
      type: mdns
      match: { host: printer.local }
  ```

  A promoted record's value is **not a snapshot** — it's resolved from the
  live discovery table at query time. If the device changes IP, the answer
  updates with it. If the device goes offline, the name resolves with **no
  answer (NODATA)** rather than a stale IP, and comes back live when the
  device reappears.

A published/promoted mDNS name takes precedence over a same-named static
record in `records.yaml` (an edge case, since you'd rarely name both the
same).

A discovered entry's "served" state at `/mdns` reflects publish/auto-publish
*and* any promoted binding that resolves to it — promoting a candidate marks
it served immediately, even though the write only touched `records.yaml`.
Once an entry is covered by a promoted binding, its publish/unpublish toggle
is hidden there: the binding already takes precedence in `Resolve`, so
toggling it would have no effect. Manage it from the Records tab instead.

## Self-advertisement (the reverse direction)

BrambleGate can also advertise *itself* via mDNS-SD, so other devices on the
LAN discover it without you typing an IP anywhere:

```yaml
mdns:
  advertise:
    enabled: false
```

This is independent of discovery — you can advertise without browsing, or
browse without advertising.

## Verifying it

**Discovery:** from a machine with `avahi-browse`/`dns-sd`, confirm a real
device (e.g. an AirPlay speaker, a network printer) announcing on the LAN
shows up as a candidate at `/mdns`. Promote it, then:

```sh
dig @<bramblegate-ip> <promoted-name>.home.arpa
```

Confirm it resolves to the device's current IP. Power the device off and
re-query — expect NODATA, not a stale cached IP. Power it back on and confirm
the answer returns.

**Self-advertisement:** from another machine on the same LAN segment, `dns-sd
-B` / `avahi-browse -a` and confirm BrambleGate's own service appears.

## Troubleshooting

**Nothing shows up as a candidate.** mDNS is link-local multicast — it does
not cross VLANs/subnets without router-level reflection (a separate,
out-of-scope feature; see your router's mDNS/Bonjour-reflector setting).
Confirm the browsing interface(s) in `mdns.interfaces` actually sit on the
same L2 segment as the device you expect to see.

**A promoted device resolves to nothing even though it's on.** Check the
`match` selector against the device's actual current announcement (host/
service/instance can change if the device is renamed or reflashed) — the
binding is exact-match against a selector, not a one-time IP capture.

**A static record and an mDNS name collide.** The mDNS answer wins. Rename
one side if you didn't intend that.