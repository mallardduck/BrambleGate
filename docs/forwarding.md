# Upstream forwarding

BrambleGate is deliberately **not an ad-blocker or recursive resolver**. Any
query it doesn't own itself (not in `home.arpa`/your configured subdomains,
not a published mDNS name) is forwarded, as-is, to whatever DNS server you
already run for that — PiHole, Technitium, AdGuard Home, or a plain public
resolver. BrambleGate never sees or touches blocklist data; it just forwards.

## Configuring it

```yaml
upstream_dns:
  address: 192.168.10.5:53   # host:port of your existing resolver
  protocol: plain             # plain | dot | doh — the internal hop to it
  ecs_enabled: false          # attach the real client IP via EDNS0 Client Subnet
```

On first run with no `settings.yaml`, BrambleGate seeds a default that
forwards to `1.1.1.1` so DNS resolves immediately out of the box — this does
**not** block ads. Point `upstream_dns.address` at your real ad-block resolver
as the first thing you do after install (`/settings` in the GUI, or edit the
file directly).

`protocol: dot`/`doh` encrypts BrambleGate's own hop to the upstream resolver
(distinct from the client-facing encrypted listeners in
[`encrypted-dns.md`](encrypted-dns.md) — that's the client-to-BrambleGate
hop; this is the BrambleGate-to-upstream hop). Most homelab setups leave this
`plain` since the upstream is usually on the same trusted network.

### EDNS0 Client Subnet (`ecs_enabled`)

When on, BrambleGate attaches the querying client's real source IP to the
forwarded query (via CoreDNS's `rewrite edns0 subnet set 32 128`, full
precision — no truncation), so `upstream_dns.address` can apply per-client
policy (e.g. PiHole/AdGuard/Technitium group-based blocking). Any
client-supplied EDNS0 Client Subnet option on the incoming query is
overwritten, not trusted.

This only makes sense — and is only allowed — when the upstream is a local
resolver you trust: `Validate` rejects `ecs_enabled: true` combined with an
upstream address that isn't a literal private/loopback/link-local IP (a
hostname or a public IP like `1.1.1.1` is rejected outright, with no
override). Sending real client IPs to a public resolver would leak them
off your network, which defeats the purpose of running BrambleGate.

## Verifying it

```sh
# A name NOT in home.arpa should come back exactly as your upstream would answer it
dig @<bramblegate-ip> <a-domain-your-blocklist-blocks>

# Confirm it matches querying the upstream directly
dig @<upstream-ip> <same-domain>
```

Expect identical results (a real answer for allowed names, `0.0.0.0`/NXDOMAIN/
whatever your blocklist tool does for blocked ones) — the forward path should
be transparent. Then confirm a name that *is* local (e.g. something in
`home.arpa`) does **not** reach the upstream at all — check your upstream's
own query log/dashboard and confirm it never saw that query.

## Troubleshooting

**Ad-blocking "stopped working."** Almost always means `upstream_dns.address`
is still the seeded `1.1.1.1` default rather than your ad-block resolver —
see the Quickstart in the root [`README.md`](../README.md).

**Local names leak to the upstream, or vice versa.** Check the query is
actually inside a zone BrambleGate owns (`home.arpa` or a configured
subdomain) — anything outside that always forwards, by design, even if it
looks like it should be "local" to you.