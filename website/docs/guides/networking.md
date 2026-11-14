---
title: Networking and network warnings
description: The Networking tab, the public-binding and Traefik badges, and the API fields behind them.
---

The **Networking** tab lists every container and the networks it is attached to.
Running containers are grouped under each network with their in-network IP and
open ports (internal then external); stopped containers appear in a separate flat
list. The data comes from `GET /api/networking`, which exposes only identity,
state, network IPs and ports — no Docker labels.

## Public port bindings ▲

A red triangle (▲) on a container card marks at least one port bound to a
non-localhost IP, such as `0.0.0.0` or a public interface. Hovering shows which
IPs are affected. Containers with every port on `127.0.0.1` or `::1` show no
warning.

**Best practice:** bind containers to `127.0.0.1` and let a reverse proxy such as
Traefik handle external traffic.

## Networking tab banner ⚠

On the Networking tab, containers that publish ports on `0.0.0.0` (all
interfaces) raise a warning banner at the top of the page listing the affected
containers, and each affected row carries a ⚠ marker. This makes services
reachable from any interface easy to spot.

## Traefik detection 🔗

A link icon (🔗) appears when a container carries `traefik.enable=true`. If it
also defines `traefik.http.routers.<name>.rule=Host(\`hostname\`)`, dumbdock
extracts the hostname and shows it as a clickable `https://` link on the card.

```yaml
labels:
  traefik.enable: "true"
  traefik.http.routers.myapp.rule: "Host(`myapp.example.com`)"
```

> **Note:** dumbdock assumes `https://` for every extracted URL, because Traefik
> usually fronts TLS-terminated services.

## API fields

`GET /api/containers` includes these per-card fields:

| Field | Type | Description |
|-------|------|-------------|
| `hasPublicBinding` | boolean | Any port is bound to a non-localhost IP |
| `publicBindingIPs` | string[] | The non-localhost IPs involved |
| `traefikEnabled` | boolean | `traefik.enable=true` is present |
| `traefikURLs` | string[] | URLs extracted from Traefik `Host` rules |
| `updateStatus` | string | `up-to-date`, `update-available`, `unknown`, or `error`; omitted until the first check completes |
| `updateAvailable` | boolean | Set only when `updateStatus` is `update-available` |
| `currentDigest` | string | Digest of the locally pulled image |
| `latestDigest` | string | Digest currently served by the registry |

## Related

- [Image update checks](./image-update-checks.md) — how `updateStatus` is decided.
- [Dashboard scoreboard](./scoreboard.md) — the tile that counts these fields.
- [Traefik dashboard](./traefik.md) — the full proxy view.
