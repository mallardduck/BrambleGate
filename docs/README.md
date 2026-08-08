# BrambleGate feature docs

These describe how to configure and use each of BrambleGate's features. For
installing and the five-minute quickstart, start at the root
[`README.md`](../README.md) instead — come here once you're ready to turn on a
specific feature.

- [`deploying-docker.md`](deploying-docker.md) — Compose/`docker run`, ports
  and volumes, updating, non-root hardening, building the image yourself.
- [`local-records.md`](local-records.md) — static `home.arpa` records and
  per-VLAN split-horizon answers.
- [`encrypted-dns.md`](encrypted-dns.md) — DoT/DoH/DoQ listeners and getting a
  real, trusted certificate via ACME DNS-01.
- [`mdns-bridge.md`](mdns-bridge.md) — discovering `.local` devices and
  bridging them into `home.arpa`, plus BrambleGate advertising itself.
- [`forwarding.md`](forwarding.md) — how upstream/ad-block forwarding works
  and how to point BrambleGate at your existing resolver.

Internal design docs (architecture, module layout, plugin internals — for
contributors, not operators) live under [`dev-docs/`](../dev-docs/).