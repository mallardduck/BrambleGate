# BrambleDNS

A small, GUI-managed **DNS front door** for a homelab. It sits in front of your
existing ad-block resolver (Pi-hole / AdGuard Home / Technitium) and adds:

- **Local records** for `home.arpa` names — managed in a web GUI, applied live with
  no restart.
- **Per-VLAN split-horizon** — the same name can resolve differently (or not at all)
  depending on which VLAN the client is on.
- **Encrypted DNS** — DoT (and DoH/DoQ) listeners with a real, auto-renewing
  certificate obtained over ACME **DNS-01** (no inbound port exposure required).
- **mDNS bridge** — discover `.local` devices and make them resolvable as stable
  `home.arpa` names.

Everything not owned locally is forwarded to your upstream ad-block resolver, so
BrambleDNS is an *addition* to your setup, not a replacement.

---

## Quickstart

You need Docker. BrambleDNS listens on port 53, so nothing else on the host may
already be bound to it (on Ubuntu, `systemd-resolved` often is — see
[Troubleshooting](#troubleshooting)).

### With Docker Compose (recommended)

```bash
# Grab the compose file
curl -O https://raw.githubusercontent.com/mallardduck/BrambleDNS/main/deploy/docker-compose.yml
docker compose up -d
```

### With `docker run`

```bash
docker run -d --name brambledns --restart unless-stopped \
  -p 53:53/udp -p 53:53/tcp -p 853:853 -p 8080:8080 \
  -v "$PWD/config:/config" \
  ghcr.io/mallardduck/brambledns:latest
```

**On first run, with no `settings.yaml`, BrambleDNS writes a working default** that
forwards everything to `1.1.1.1` — so DNS resolves immediately. Then:

1. Open the GUI at **http://localhost:8080**.
2. Set **Upstream DNS** to your existing ad-block resolver (e.g. `192.168.10.5:53`)
   and save. This is the one change that makes ad-blocking work again.
3. Add local records, and point a client (or your router's DHCP DNS option) at the
   host running BrambleDNS.

That's the whole loop. Nothing below is required reading — it's reference.

---

## Configuration

Config lives in the mounted `/config` volume as two hand-editable YAML files that
the GUI also manages. Copy the annotated examples to start from scratch:

- **[`deploy/example/settings.yaml`](deploy/example/settings.yaml)** — upstream,
  listeners, VLANs, ACME, mDNS.
- **[`deploy/example/records.yaml`](deploy/example/records.yaml)** — your
  `home.arpa` records and their per-VLAN overrides.

Generated runtime files live under `/config/.runtime/` and issued certs under
`/config/certs/`; you don't edit those.

### `settings.yaml` at a glance

| Key | What it does |
| --- | --- |
| `upstream_dns.address` | `host:port` of your ad-block resolver. Everything not local is forwarded here. |
| `upstream_dns.protocol` | `plain` (default), `dot`, or `doh` for the internal hop. |
| `listeners.plain` / `.dot` | Enable/port for each transport. Plain `:53` is on by default. |
| `vlans` | Your real VLAN name → CIDR(s) mapping, used for split-horizon and mDNS scoping. |
| `acme` | Encrypted-DNS certificate issuance (see below). Off by default. |
| `mdns` | `.local` discovery bridge. Off by default. |

### Split-horizon records

A record has a `default` value plus optional `vlan_overrides`. Each override can
give a VLAN a different value, a different TTL, or `nxdomain: true` to hide the name
from that VLAN entirely:

```yaml
records:
  - name: nas.home.arpa
    type: A
    default: 192.168.10.20
    vlan_overrides:
      - vlan: untrusted-wifi
        nxdomain: true      # guests can't see the NAS at all
```

### Encrypted DNS (DoT/DoH) with a real certificate

Enable a `dot` (or `doh`) listener and the `acme` block. BrambleDNS obtains a
certificate over **DNS-01**, so it never needs an inbound port open — it only needs
outbound access to the ACME server and your DNS provider's API. It starts serving on
a self-signed cert immediately and hot-swaps the real cert in when it lands.

Set `acme.dns_provider` and export that provider's credentials **as environment
variables** (never in the config file). First-class providers:

| `dns_provider` | Primary env vars |
| --- | --- |
| `cloudflare` | `CLOUDFLARE_DNS_API_TOKEN` |
| `route53` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` |
| `gcloud` | `GCE_PROJECT` (+ service-account creds) |
| `azuredns` | Azure service-principal vars |
| `digitalocean` | `DO_AUTH_TOKEN` |
| `linode` | `LINODE_TOKEN` |
| `hetzner` | `HETZNER_API_KEY` |
| `ovh` | OVH application/consumer keys |
| `namecheap` | `NAMECHEAP_API_USER`, `NAMECHEAP_API_KEY` |
| `rfc2136` | `RFC2136_NAMESERVER`, `RFC2136_TSIG_*` |
| `exec` / `httpreq` | custom script / webhook — no provider SDK needed |

Leave `acme.production: false` (the default) while testing — it uses Let's
Encrypt's staging CA, which issues **untrusted** certs so you can't burn production
rate limits. Flip it to `true` once issuance succeeds.

---

## Updating

```bash
docker compose pull && docker compose up -d
# or, for docker run:
docker pull ghcr.io/mallardduck/brambledns:latest && docker restart brambledns
```

Your `/config` volume carries all state across updates.

---

## Troubleshooting

**Port 53 is already in use.** On many Linux hosts `systemd-resolved` binds `:53`.
Free it by setting `DNSStubListener=no` in `/etc/systemd/resolved.conf` and
restarting the service, or run BrambleDNS on a host/interface that isn't already a
resolver.

**Ad-blocking stopped working.** The seeded default forwards to `1.1.1.1`, which does
*not* block ads. Set `upstream_dns.address` to your Pi-hole/AdGuard and save.

**A phone rejects the DoT certificate.** Strict clients (Android Private DNS) reject
the self-signed bootstrap cert. Enable ACME with a real domain + provider credentials
and wait for the real cert to be issued (`production: true` once staging works).

**A GUI edit didn't take effect.** Saves validate before writing; an invalid edit is
rejected with the reason and the previous config keeps serving. Check the container
logs (`docker logs brambledns`) for the validation or reload error.

---

## Running as non-root (hardening)

The published image runs as **root** by default, deliberately: it binds the
privileged port 53 and seeds/rewrites config on a freshly mounted `/config`, both of
which are awkward for an unprivileged uid on a fresh host. This matches how
Pi-hole/dnsmasq images ship.

To run unprivileged instead:

1. `chown` your config directory to the uid you'll run as.
2. Grant just the bind capability and set the user, e.g. in compose:
   ```yaml
   user: "1000:1000"
   cap_add: ["NET_BIND_SERVICE"]
   ```
   …or map the listeners to high ports in `settings.yaml` and publish those.

---

## For developers

Design docs live under [`docs/`](docs/) (architecture, DNS engine, plugins, config
schema, certificates, roadmap). The repo is a multi-module Go workspace — see
[`docs/repo-layout.md`](docs/repo-layout.md) for the module layout and the
`replace`-vs-`go.work` build model.
