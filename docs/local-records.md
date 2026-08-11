# Local records and split-horizon

BrambleGate is authoritative for a fixed set of zones — `home.arpa` (and any
subdomains of it you use, e.g. `k8s.home.arpa`), plus your ACME domain if
configured and `resolver.arpa` if you have DDR records. Records you add here
are answered directly — they never go to your upstream ad-block resolver.

Unlike PiHole's arbitrary local-DNS-records feature, you cannot point a record
at any domain name you like (e.g. `pihole.lan` or a real public domain).
Saving a record whose name falls outside the owned zones is rejected at
validation time with an "outside the owned zones" error — the write never
reaches `records.yaml`. The GUI's `/records` page lists the currently owned
zones. Supporting arbitrary custom zones is a potential future feature, not
implemented today.

## Adding a record

**GUI:** `/records` — add, edit, or delete a record; changes apply immediately,
no restart or reload delay from your perspective.

**By hand:** edit `/config/records.yaml` (the GUI writes the same file — see
[atomic write rules](../dev-docs/config-schema.md#atomic-write-rules) if you're
curious how concurrent GUI/hand edits stay safe).

```yaml
records:
  - name: nas.home.arpa
    type: A
    default: 192.168.10.20
    ttl: 300              # optional; omitted -> 300s server default
```

Supported types: `A`, `AAAA`, `CNAME`, plus the special `type: mdns` (see
[`mdns-bridge.md`](mdns-bridge.md) — those are created by "promote," not
hand-written).

## Per-VLAN split-horizon

Declare your real VLANs in `settings.yaml` first (name + one or more CIDRs —
BrambleGate doesn't create VLANs, it just needs to know which subnet is which):

```yaml
vlans:
  - name: trusted
    cidrs: [192.168.10.0/24]
  - name: untrusted-wifi
    cidrs: [192.168.30.0/24]
```

Then give a record per-VLAN overrides. Each override is a *partial* adjustment
— specify only what differs from the record's `default`:

```yaml
records:
  - name: nas.home.arpa
    type: A
    default: 192.168.10.20
    vlan_overrides:
      - vlan: untrusted-wifi
        nxdomain: true        # guest wifi gets no answer at all
      - vlan: smarthome
        value: 192.168.20.5   # a different address on this VLAN
        ttl: 60                # and a shorter TTL there
```

- `nxdomain: true` — this VLAN gets no answer (authoritative NXDOMAIN).
  Mutually exclusive with `value`/`ttl`.
- `value` — this VLAN's answer instead of `default`. Omit to inherit
  `default` (useful for a TTL-only override).
- `ttl` — this VLAN's TTL. Omit to inherit the record's effective TTL.

Which VLAN a client is "on" is decided by matching its source IP against the
declared CIDRs, in `settings.yaml` order — first match wins. A client that
doesn't match any declared VLAN gets the record's plain `default`.

**Split-horizon answers are never cached.** Answering the same name
differently per VLAN would break if CoreDNS's cache served one VLAN's answer
to another, so these lookups run ahead of the cache in the chain. Only
forwarded (upstream) answers get cached.

## Verifying it

From a client on a given VLAN:

```sh
dig @<bramblegate-ip> nas.home.arpa
```

Confirm the answer (or the NXDOMAIN) matches what that VLAN's override says.
Repeat from a client on a different declared VLAN and confirm the answer
differs as configured — this is the split-horizon check to run from real
devices on real VLANs (not just from the BrambleGate host itself, which won't
land in any of your VLAN CIDRs unless you're on one).

## Troubleshooting

**A record won't save.** Confirm the name is actually under one of the owned
zones (`home.arpa` or a configured subdomain, the ACME domain, or
`resolver.arpa`) — a name outside all of them is rejected at save time with an
"outside the owned zones" error, not silently accepted or forwarded.

**Every VLAN gets the same answer.** Check the client's actual source subnet
against your declared CIDRs — a device with a static IP outside the VLAN's
declared range, or a VLAN missing from `settings.yaml`, silently falls back to
`default` rather than erroring.

**An edit didn't take effect.** Saves are validated before being written; an
invalid edit (overlapping VLAN CIDRs, `nxdomain` combined with `value`, an
override referencing an undeclared VLAN, etc.) is rejected with the reason and
the previous config keeps serving. Check `docker logs bramblegate`.