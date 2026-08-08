# Encrypted DNS (DoT / DoH / DoQ) and real certificates

BrambleGate can serve DNS over TLS, DNS over HTTPS, and DNS over QUIC, in
addition to plain `:53`. The listeners work with no certificate setup at all
(self-signed, bootstrap cert) — but strict clients (Android's Private DNS is
the classic case) refuse a self-signed cert, so getting a *real* one is what
makes this feature actually usable from a device.

## Enabling a listener

`settings.yaml`:

```yaml
listeners:
  plain:
    enabled: true
    port: 53
  dot:
    enabled: true
    port: 853
  doh:
    enabled: true
    port: 443
  doq:
    enabled: false
    port: 8853
```

(Also editable from `/settings` in the GUI.) Each transport is independently
on/off with its own port. As soon as a `dot`/`doh`/`doq` listener is enabled,
BrambleGate starts serving on a **self-signed bootstrap cert** immediately —
good enough to test that the listener itself works, not good enough for a
device that validates the chain.

## Getting a real certificate (ACME DNS-01)

BrambleGate never needs an inbound port opened for this — it proves domain
ownership via a DNS TXT record instead (DNS-01), so it only needs *outbound*
access to the ACME server and your DNS provider's API.

```yaml
acme:
  enabled: true
  domain: dns.example.com   # a real domain you own; its A record may point at a LAN IP
  email: you@example.com
  dns_provider: cloudflare
  production: false         # false = Let's Encrypt STAGING (untrusted cert, no rate limits)
  renew_before_days: 30
```

Provider credentials are **environment variables on the container, never in
`settings.yaml`**. Supported `dns_provider` values and their primary env vars:

| `dns_provider` | Primary env vars |
| --- | --- |
| `cloudflare` | `CLOUDFLARE_DNS_API_TOKEN` |
| `route53` (alias `aws`) | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` |
| `gcloud` (aliases `google`, `googlecloud`) | `GCE_PROJECT`, `GCE_SERVICE_ACCOUNT` |
| `azuredns` (alias `azure`) | `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`, `AZURE_RESOURCE_GROUP` |
| `digitalocean` (alias `do`) | `DO_AUTH_TOKEN` |
| `linode` | `LINODE_TOKEN` |
| `hetzner` | `HETZNER_API_KEY` |
| `ovh` | `OVH_ENDPOINT`, `OVH_APPLICATION_KEY`, `OVH_APPLICATION_SECRET`, `OVH_CONSUMER_KEY` |
| `namecheap` | `NAMECHEAP_API_USER`, `NAMECHEAP_API_KEY` |
| `rfc2136` (alias `dnsupdate`) | `RFC2136_NAMESERVER`, `RFC2136_TSIG_KEY`, `RFC2136_TSIG_SECRET`, `RFC2136_TSIG_ALGORITHM` |
| `exec` | `EXEC_PATH` — run your own script, no provider SDK needed |
| `httpreq` | `HTTPREQ_ENDPOINT` — call your own webhook |

**Stay on `production: false` until issuance succeeds once.** Staging has no
rate limits but issues a cert your device won't trust; only flip to
`production: true` after you've confirmed the staging flow completes cleanly
(check `docker logs bramblegate` for the issuance result), so you don't burn
Let's Encrypt's production rate limits debugging a provider-credential typo.

Once issued, BrambleGate **hot-swaps** the real cert into the running
listeners with no restart, and renews automatically starting
`renew_before_days` before expiry.

## Verifying it

From the BrambleGate host or any machine with `openssl`/`kdig`:

```sh
# Confirm the chain and CN/SAN match your domain
openssl s_client -connect <bramblegate-ip>:853 -servername dns.example.com </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates

# A DoT query, if you have kdig/knot-utils
kdig @<bramblegate-ip> +tls nas.home.arpa
```

The real, device-level check is Phase 7 of the roadmap: point a stock
Android phone's Private DNS setting at `dns.example.com`, with
`production: true` and a valid cert, and confirm it validates with **no
trust-store changes** on the phone. That's the test this whole feature exists
to pass — self-signed or staging certs will not satisfy it.

## Troubleshooting

**Phone rejects the certificate / "Private DNS server cannot be accessed."**
Almost always means the phone is still seeing the self-signed bootstrap cert
or a staging (untrusted) cert. Confirm `acme.production: true` and that
issuance actually succeeded in the logs before assuming it's a network issue.

**Issuance never completes.** Check the DNS provider credentials are present
as environment variables (not in `settings.yaml` — they're deliberately never
read from there), and that the TXT record actually propagates — some
providers/registrars are slow; `renew_before_days` gives headroom but initial
issuance has no such grace.

**Using a private CA or Pebble instead of Let's Encrypt** (e.g. for testing):
set `acme.ca_directory_url` to override the ACME server. This is how the
project's own `acme_integration` test runs against Pebble.