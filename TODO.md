# dumbdock

A no-frills Docker dashboard. Configure cards via labels, see everything else (labeled or not) so nothing running is invisible.

## Goal

- Read Docker socket(s), list all running containers.
- Containers with `dumbdock.*` labels get rendered as configured cards (name, icon, link, group).
- Containers without labels still show up, dumped into an "unlabeled" section, so nothing gets hidden by omission.
- No database, no auth system, no plugin ecosystem. One binary/container, one socket mount, done.

## Label schema (draft)

```
dumbdock.enable=true       # optional, only needed if you want explicit opt-in instead of opt-out
dumbdock.name=Dozzle
dumbdock.group=Monitoring
dumbdock.icon=dozzle.svg    # or a URL
dumbdock.href=https://dozzle.lmthosting.com
dumbdock.description=Container log viewer
```

Decide: opt-in (only show containers with at least `dumbdock.name`) vs opt-out (show all, unlabeled get default card). Leaning opt-out since that's the whole point of this tool.

## MVP scope

- [ ] Single Go or Node service, polls `/var/run/docker.sock` every N seconds (or uses Docker events API to update live)
- [ ] Parses container labels matching `dumbdock.*` prefix
- [ ] Renders simple grid UI: grouped cards for labeled containers, flat list for unlabeled
- [ ] Unlabeled containers show: name, image, status, ports — enough to identify and go label it
- [ ] Single docker-compose.yml, mounts socket read-only
- [ ] No auth in v1 (assumes internal network / behind existing reverse proxy + auth layer like you already run)

## Stretch / v2 ideas

- [ ] Multi-host support (reuse docker-socket-proxy pattern, since you already use it for PruneMate-style tools)
- [ ] ntfy/Gotify alert when a new unlabeled container appears (closes the loop — turns this into a passive labeling-compliance nudge)
- [ ] Group ordering / custom sort via label (`dumbdock.order=1`)
- [ ] Simple static config override file for things you can't label (e.g. someone else's container you don't control)
- [ ] Dark theme matching the rest of the homelab stack

## Explicit non-goals

- No multi-user accounts
- No built-in reverse proxy or SSL termination
- No metrics/graphing (that's Beszel's job)
- No log viewing (that's Dozzle's job)
- No deploy/start/stop actions (that's Portainer/Dockge's job) — this is read-only visibility

## Open questions

- Go vs Node vs just a static HTML page with a tiny backend? Given "low overhead" is the whole point, lean toward whichever you already have boilerplate/familiarity with.
- Docker socket directly, or go through docker-socket-proxy from day one for consistency with the rest of the stack?
- Where does this live — gitea.optikhosting.com new repo?
