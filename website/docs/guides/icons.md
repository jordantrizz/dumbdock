---
title: Icons
description: The built-in icon sets, the resolution order, how to add custom sets, and what auto-detection does.
---

dumbdock ships with two built-in icon sets, enabled by default:

1. **[selfhst/icons](https://github.com/selfhst/icons)** — served from
   `https://cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg`. A curated map
   (`icons.json`) embedded in the binary maps common image names to icon slugs;
   the live index is fetched at startup for up-to-date slug discovery.
2. **[dashboard-icons](https://github.com/homarr-labs/dashboard-icons)** — served
   from `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/{slug}.svg`.
   The index (`tree.json`) is fetched at startup and needs no static mapping,
   because slugs match image names 1:1.

When a container has no explicit `dumbdock.icon` label, dumbdock derives a slug
from the image name (`portainer/portainer-ce:latest` becomes `portainer`) and
checks each configured set in priority order. The first match wins. With no match
anywhere, a best-guess URL from the highest-priority set is returned and a generic
placeholder is shown.

## Resolution order

1. **Full image path in static mappings** — `portainer/portainer-ce` maps to
   `portainer`.
2. **Last path component in static mappings** — `portainer-ce` maps to
   `portainer`.
3. **Last path component as a known slug** — `nginx` matches a known icon name.
4. **Best-guess URL** — falls back to the highest-priority icon set.

## Custom icon sets

Replace the built-in sets entirely with an `iconSets` array in `dumbdock.json`:

```json
{
  "iconSets": [
    {
      "name": "selfhst",
      "indexUrl": "https://cdn.jsdelivr.net/gh/selfhst/icons@main/index.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg",
      "indexFormat": "map",
      "mappings": {
        "actualbudget": "actual-budget",
        "nginx": "nginx"
      },
      "priority": 0
    },
    {
      "name": "dashboard-icons",
      "indexUrl": "https://raw.githubusercontent.com/homarr-labs/dashboard-icons/refs/heads/main/tree.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/{slug}.svg",
      "indexFormat": "tree",
      "indexField": "svg",
      "priority": 1
    },
    {
      "name": "my-custom-set",
      "indexUrl": "/config/my-icons/index.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/my/icons/svg/{slug}.svg",
      "indexFormat": "list",
      "priority": 2
    }
  ]
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | — | Identifier used in diagnostics |
| `indexUrl` | string | `""` | URL or local path for the index: `http://`, `https://`, an absolute path, or `file://`. Empty skips index fetching. |
| `cdnUrlTemplate` | string | — | CDN URL containing a `{slug}` placeholder |
| `indexFormat` | string | `"map"` | `"map"` for a flat `{"key": "slug"}` object, `"list"` for an array of slugs, `"tree"` for an object holding a filename array |
| `indexField` | string | `"svg"` | For `"tree"`, the field holding the filename array |
| `mappings` | object | `{}` | Static image-key to icon-slug mappings; these beat index resolution |
| `priority` | number | `0` | Lower is checked first; first match wins |

> **Note:** when `iconSets` is present, the built-in defaults are **not**
> automatically included. To keep both defaults plus custom sets, duplicate the
> default entries in your config.

## Auto-detection

When `AUTO_DETECTION` is enabled (the default), unlabeled containers whose image
name matches an entry in `icons.json` are placed in the "Auto Detected" group,
which keeps them organised and visible. Containers without a matching icon stay
in the "Unlabeled" section.

dumbdock detects its own containers (`dumbdock`, `jordantrizz/dumbdock`,
`ghcr.io/jordantrizz/dumbdock`) and shows them with the dumbdock icon.

Behaviour is customisable through `AUTO_DETECTION`, `AUTODETECT_GROUP_NAME`, and
`AUTO_DETECTION_SERVICE_BLACKLIST`, or the `autoDetection`, `autoDetectedGroup`
and `autoDetectionServiceBlacklist` config settings. Config-file overrides take
precedence: a container with an explicit config entry lands in its configured
group regardless of auto-detection.

## Related

- [Your first dashboard](../getting-started/first-dashboard.md) — setting an
  explicit `dumbdock.icon`.
- [Network warnings](./networking.md) — the other badges on a card.
