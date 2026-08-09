# Deploying with Docker

BrambleGate ships as a single multi-arch (`linux/amd64`, `linux/arm64`) image,
built from [`deploy/Dockerfile`](../deploy/Dockerfile) and published to GHCR
on every tagged release. There's nothing else to install — no database, no
sidecar containers required.

```
ghcr.io/mallardduck/bramblegate:latest
```

## Docker Compose (recommended)

Copy [`deploy/docker-compose.yml`](../deploy/docker-compose.yml):

```bash
curl -O https://raw.githubusercontent.com/mallardduck/BrambleGate/main/deploy/docker-compose.yml
docker compose up -d
```

```yaml
services:
  bramblegate:
    image: ghcr.io/mallardduck/bramblegate:latest
    container_name: bramblegate
    restart: unless-stopped
    ports:
      - "53:53/udp"
      - "53:53/tcp"
      - "853:853/tcp"   # DoT — only used once enabled in settings.yaml
      - "8080:8080/tcp" # web GUI + JSON API
    volumes:
      - ./config:/config
    # environment:
    #   CLOUDFLARE_DNS_API_TOKEN: ${CLOUDFLARE_DNS_API_TOKEN}
```

## `docker run` equivalent

```bash
docker run -d --name bramblegate --restart unless-stopped \
  -p 53:53/udp -p 53:53/tcp -p 853:853 -p 8080:8080 \
  -v "$PWD/config:/config" \
  ghcr.io/mallardduck/bramblegate:latest
```

## Ports and volumes

| Port | Protocol | Purpose |
| --- | --- | --- |
| 53 | UDP + TCP | Plain DNS. Point clients/your router's DHCP DNS option here. |
| 853 | TCP | DoT. Only actually listens once `listeners.dot.enabled: true`. |
| 8080 | TCP | Web GUI + JSON API. |

`doh` (default port 443) and `doq` (default port 8853) aren't published by
default in the example compose file — add `-p`/a `ports:` entry for them if
you enable those listeners (see [`encrypted-dns.md`](encrypted-dns.md)).
Whatever ports you enable in `settings.yaml`, make sure the matching
container port is published — the container binds what's configured, but a
port only reaches the LAN if it's also exposed on the host.

`/config` is the **only** state that matters: `settings.yaml`, `records.yaml`,
issued ACME certs, and generated runtime files (Corefile, zone data) all live
there. A bind mount (`./config:/config`, as in the example) keeps those files
editable from the host; swap for a named volume if you'd rather not.

## First run

With no `settings.yaml` present, BrambleGate seeds a working default — plain
`:53` forwarding to `1.1.1.1` — so DNS resolves immediately instead of
requiring you to hand-write YAML before the container is useful at all. Then:

1. Open `http://<host>:8080`.
2. Set **Upstream DNS** to your real ad-block resolver and save (see
   [`forwarding.md`](forwarding.md)) — the one change that makes ad-blocking
   work again, since the seeded default doesn't block anything.
3. Add records, enable encrypted listeners/mDNS as you need them.

## Updating

```bash
docker compose pull && docker compose up -d
# or, for docker run:
docker pull ghcr.io/mallardduck/bramblegate:latest && docker restart bramblegate
```

`/config` carries all state across updates — nothing to migrate by hand.

## Running as non-root (hardening)

The image runs as **root** by default, deliberately: it needs to bind the
privileged port 53 and seed/rewrite `settings.yaml`/`records.yaml` on a
freshly bind-mounted `/config`, both awkward for an unprivileged uid on a
fresh host (no `CAP_NET_BIND_SERVICE`, `/config` owned by host root). This
matches how Pi-hole/dnsmasq homelab images ship.

To run unprivileged instead:

1. `chown` your config directory to the uid you'll run as.
2. Grant just the bind capability and set the user, e.g. in compose:
   ```yaml
   user: "1000:1000"
   cap_add: ["NET_BIND_SERVICE"]
   ```
   …or map the listeners to high ports in `settings.yaml` and publish those
   instead of touching capabilities at all.

## Building the image yourself

`deploy/Dockerfile` is a two-stage build: stage 1 (`golang:1.26`) compiles
the root module — its `go.mod` `replace` directives pull in `engine/` and
both `plugins/*` from disk, so the whole repo needs to be in the build
context, and `go.work*` is excluded via `.dockerignore` so the build runs in
plain-module + `replace` mode, not workspace mode. Stage 2 is
`distroless/static-debian12` — just the binary and CA certs (needed for
ACME), plus (by default) a `busybox` shell for debugging. The binary is pure
Go (`CGO_ENABLED=0`), so cross-compiling to `arm64` from an `amd64` builder
(or vice versa) is a fast native compile, not QEMU-emulated.

```bash
docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile -t bramblegate:local .
```

The runtime base image is picked via the `VARIANT` build arg, which defaults
to `debug` (a distroless tag that includes a busybox shell, so
`docker exec -it <container> sh` works). Build with
`--build-arg VARIANT=latest` for the true minimal, shell-less image used in
production.

Release builds pass `VARIANT` based on the tag: prerelease tags
(alpha/beta/rc) keep the debug shell, real stable releases build with
`VARIANT=latest`. This happens via `docker/build-push-action` in
`.github/workflows/release.yml`, gated on CI passing first — see
`dev-docs/roadmap.md` Phase 6.

## Troubleshooting

**Port 53 is already in use on the host.** Common on Linux hosts where
`systemd-resolved` binds `:53`. Either free it (`DNSStubListener=no` in
`/etc/systemd/resolved.conf`, then restart the service) or run BrambleGate on
a host/interface that isn't already acting as a resolver.

**Container starts but the GUI/DNS isn't reachable from other devices.**
Check the port is both enabled in `settings.yaml` *and* published in the
`docker run`/compose `ports:` — enabling a listener in config doesn't publish
it from the container automatically.

**Config changes made on the host aren't picked up.** The GUI writes
`settings.yaml`/`records.yaml` via write-temp-then-rename in the same
directory, which requires `/config` to be a single filesystem/mount (true for
both bind mounts and named volumes) — if you've split `/config` across
multiple mounts, atomic writes break. Keep it as one mount.