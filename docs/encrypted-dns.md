# Encrypted DNS (DoT / DoH / DoQ / DoH3) and real certificates

BrambleGate can serve DNS over TLS, DNS over HTTPS, DNS over QUIC, and DNS
over HTTP/3, in addition to plain `:53`. The listeners work with no
certificate setup at all (self-signed, bootstrap cert) — but strict clients
(Android's Private DNS is the classic case) refuse a self-signed cert, so
getting a *real* one is what makes this feature actually usable from a
device.

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
    port: 853   # UDP — safe to share with dot's port, since dot is TCP-only
  doh3:
    enabled: false
    port: 443   # UDP — safe to share with doh's port, since doh is TCP-only
```

(Also editable from `/settings` in the GUI, which fills these same defaults
in as soon as you enable a listener.) Each transport is independently on/off
with its own port. DoQ and DoT can share port 853 because the OS keeps TCP
and UDP socket bindings separate — DoT is TCP-only, DoQ is UDP-only, so
there's no collision. The same goes for DoH (TCP) and DoH3 (UDP) sharing 443.
As soon as a `dot`/`doh`/`doq`/`doh3` listener is enabled, BrambleGate starts
serving on a **self-signed bootstrap cert** immediately — good enough to test
that the listener itself works, not good enough for a device that validates
the chain.

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

**This is DNS-01, not HTTP-01** — the ACME CA never connects to your box at
all. It reads a TXT record published via your DNS provider's API instead, so
BrambleGate only ever makes *outbound* calls (to the ACME server and the
provider API). Nothing needs to be port-forwarded on your router, and the
`domain`'s `A` record can point at a private LAN address. If you've seen
"open port 80 for Let's Encrypt" advice elsewhere, that's HTTP-01 (e.g.
certbot's standalone mode) — a different, unused-here challenge type.

**A note on Android's manual Private DNS mode specifically:** it does a
bootstrap hostname→IP lookup over the public DNS system before attempting the
TLS handshake, so `domain` needs to be resolvable by a public resolver for
that mode to work at all — a name BrambleGate only answers inside its own
served zone isn't enough, even from a device on the same LAN. That said, do
**not** "fix" this by publishing a private/RFC1918 address in the domain's
public zone — `draft-ietf-dnsop-dontpublish-unreachable` documents this as a
real anti-pattern (private addresses are ambiguous outside your own LAN), and
it doesn't actually satisfy the spirit of the requirement anyway. Getting
Android's strict mode working against a LAN-only resolver without exposing it
to the public internet is an open design question, not yet solved here.

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
| `namecheap` | `NAMECHEAP_API_USER`, `NAMECHEAP_API_KEY` — API access must be enabled on the Namecheap account, and the container's *outbound* IP whitelisted in Namecheap's dashboard (Namecheap authenticates by caller IP as well as key). The key is account-wide, not scoped to one domain. |
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

DoQ and DoH3 are UDP, so they aren't reachable via a plain `openssl s_client`
TCP handshake even when the listener is healthy — use a client that speaks
QUIC/HTTP-3 (e.g. `kdig +quic` for DoQ) to test those.

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