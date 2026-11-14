---
title: Grouping by dependency
description: Group the dashboard by label group, Compose project, or the depends_on label Docker Compose sets automatically.
---

The dashboard groups containers three ways, cycled by the toggle in the nav bar:
**By Group → By Compose → By Dependency → By Group**. The choice is remembered
across page loads, and the toggle is only shown on the Dashboard tab because it
affects only the container grid.

## By dependency

The dependency view reads the `com.docker.compose.depends_on` label that Docker
Compose sets automatically — no `dumbdock.*` label is required.

Containers are grouped under the container(s) they depend on, and the group
heading is the dependency's display name (`dumbdock.name` when set, otherwise the
container name). With this compose file:

```yaml
services:
  db:
    image: postgres:16
  app:
    image: myapp
    depends_on:
      - db
```

the **By Dependency** view shows `app` under a `db` heading.

## Behaviour notes

- **Direct dependencies only** — grouping uses the immediate `depends_on`
  targets with no transitive resolution. A container that depends on `api`,
  which itself depends on `db`, is grouped under `api`, not `db`.
- **Multiple dependencies** — a container that depends on several services
  appears in each of those dependency groups.
- **No resolvable dependency** — containers with no `depends_on` label, or whose
  dependency is not running, go under a `Standalone` group.
- **Switching views** — the grouping mode is independent of the tab; leaving the
  Dashboard hides the toggle without changing the saved mode.

## Related

- [Your first dashboard](../getting-started/first-dashboard.md) — the label-based
  group.
