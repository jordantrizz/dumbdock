# dumbdock

A no-frills web dashboard for your running Docker containers, driven by labels.

**Documentation: https://jordantrizz.github.io/dumbdock/**

<!-- BEGIN docs-index -->
| Section | Link |
| --- | --- |
| Getting started | https://jordantrizz.github.io/dumbdock/getting-started/overview |
| Guides | https://jordantrizz.github.io/dumbdock/guides/traefik |
| Commands | https://jordantrizz.github.io/dumbdock/commands/control-sh |
| Reference | https://jordantrizz.github.io/dumbdock/reference/api |
| Contributing | https://jordantrizz.github.io/dumbdock/contributing/development |
| History | https://jordantrizz.github.io/dumbdock/history/changelog |
<!-- END docs-index -->

## Quick start

```bash
./control.sh setup
```

Open `http://localhost:8080`. The default auth mode is `web-auth`; with no
`DUMBDOCK_PASSWORD` set, a random password is generated for the run and logged
once at startup:

```bash
docker compose logs dumbdock | grep -i password
```

## What you get

- **Label-driven cards** — name, group, icon, link, description and order from
  `dumbdock.*` labels, or from a JSON config file.
- **Networking** — every container's networks, in-network IP and ports, with
  warnings for `0.0.0.0` bindings.
- **Traefik** — routers, services, middlewares, entrypoints and TLS certificates
  from Traefik v3's API, with a bounded boot attempt gate.
- **Issues** — provable problems only: public ports, image updates, unhealthy
  containers, proxy failures, misplaced data, stale or missing backups.
  Acknowledge one until it resolves, and open a type's Help reference from
  its info control.
- **Storage and backups** — where each compose file and database lives, and how
  fresh its last verified backup is.
- **Nothing is mutated** — dumbdock reads the Docker and Traefik APIs and writes
  only its own state file.

## Labelling a container

```yaml
labels:
  dumbdock.name: "My Nginx"
  dumbdock.group: "Web Servers"
  dumbdock.href: "https://myapp.example.com"
```

Anything unlabelled still appears, in the unlabeled section, with copy-paste
examples.

## Development

```bash
go build -o dumbdock .
go test ./...
```

The docs site lives in `website/` (`cd website && npm ci && npm run build`).
Contributor conventions — including the docs gate — are in
[`AGENTS.md`](AGENTS.md).

## Contributing

Read [`AGENTS.md`](AGENTS.md) first: it records the per-feature contracts, the
build rules, and the security findings and their fixes. AI-assisted development
and the human review it requires are documented on the site.

## License

MIT. See the [license page](https://jordantrizz.github.io/dumbdock/contributing/license).
