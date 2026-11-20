---
title: Icons
description: The built-in icon sets, how the image name becomes an icon URL, and how to add, override or disable a set.
---

dumbdock ships with two icon sets, enabled by default:

| Set | Index | Images |
|-----|-------|--------|
| **selfhst** | `cdn.jsdelivr.net/gh/selfhst/icons@main/index.json` | `cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg` |
| **dashboard-icons** | `raw.githubusercontent.com/homarr-labs/dashboard-icons/main/metadata.json` | `cdn.statically.io/gh/homarr-labs/dashboard-icons/main/{base}/{slug}.{base}` |

The two sets use different hosts for a reason. dashboard-icons is larger than
jsDelivr's 50 MB package limit, so jsDelivr answers `403` for any file it has not
already cached; its images are served by statically.io instead. Its index stays
on `raw.githubusercontent.com` because statically.io stalls on the 1.3 MB
`metadata.json`. selfhst is small and stays on jsDelivr.

## How an image name becomes an icon URL

When a container has no explicit `dumbdock.icon` label, dumbdock derives a name
from the image (`portainer/portainer-ce:latest` becomes `portainer-ce`) and checks
each set in priority order. Within a set, the first match wins:

1. **Full image path in static mappings** — `portainer/portainer-ce` maps to
   `portainer`.
2. **Last path component in static mappings** — `portainer-ce` maps to
   `portainer`.
3. **Exact slug** — `nginx` matches the `nginx` icon.
4. **Normalized slug** — case, spaces and underscores are folded, so
   `Nextcloud` matches `nextcloud`.
5. **Normalized alias** — the aliases an index declares, so `WireGuard` matches
   `wireguard`, `kali` matches `kali-linux`, and `airtrail` matches `air-trail`.

With no match anywhere, a best-guess URL from the highest-priority set is
returned and a generic placeholder is shown.

## Icon formats

`metadata.json` records whether an icon is an `svg` or a `png`; a few hundred
icons exist only as png. The index is what makes the right file URL possible:

- `{slug}` is replaced with the icon slug.
- `{base}` is replaced with the icon's format (`svg`, `png` or `webp`).
- `{ext}` is the same value as `{base}`, for indexes that name the extension
  separately.

A template that uses only `{slug}` behaves exactly as before, so an older custom
set keeps working. An icon the index does not describe is treated as `svg`.

## Adding, overriding and disabling sets

`iconSets` in `dumbdock.json` **extends** the built-ins instead of replacing
them:

- an entry whose `name` matches a built-in (case-insensitive) **overrides** that
  set; empty fields are inherited;
- a new `name` **adds** a set;
- `"enabled": false` **removes** a set.

To keep only your own set, disable both built-ins.

```json
{
  "iconSets": [
    {
      "name": "my-icons",
      "indexUrl": "/config/my-icons/index.json",
      "cdnUrlTemplate": "https://icons.example.com/{base}/{slug}.{ext}",
      "indexFormat": "metadata",
      "priority": 2
    },
    {
      "name": "dashboard-icons",
      "cdnUrlTemplate": "https://my-mirror.example.com/{base}/{slug}.{base}"
    },
    {
      "name": "selfhst",
      "enabled": false
    }
  ]
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | — | Identifier, and the key that overrides a built-in |
| `enabled` | boolean | `true` | `false` removes the set |
| `indexUrl` | string | `""` | `https://`, `http://`, an absolute path, or `file://`; empty skips index fetching |
| `cdnUrlTemplate` | string | — | Image URL containing `{slug}`, and optionally `{base}`/`{ext}` |
| `indexFormat` | string | `"map"` | `map`, `list`, `tree`, `objects` or `metadata` |
| `indexField` | string | `"svg"` | Field holding filenames (`tree`) or the slug (`objects`) |
| `mappings` | object | `{}` | Static image-key to slug mappings; these beat index resolution |
| `priority` | number | `0` | Lower is checked first; equal priorities keep built-ins first |

## Index formats

| Format | Shape |
|--------|-------|
| `map` | `{"image-name": "slug", …}` — the values are the slugs |
| `list` | `["slug", …]` |
| `tree` | `{"svg": ["name.svg", …]}` — `indexField` selects the array and sets the base |
| `objects` | `[{"Reference": "slug"}, …]` — `indexField` names the slug key |
| `metadata` | `{"slug": {"base": "png", "aliases": ["…"], "colors": {}, "categories": []}}` |

Indexes are capped at 16 MiB, local and remote alike; a larger index is refused
and the set keeps its static mappings.

## Auto-detection

When `AUTO_DETECTION` is enabled (the default), unlabeled containers whose image
name resolves to an icon are placed in the "Auto Detected" group, which keeps
them organised and visible. Containers without a matching icon stay in the
"Unlabeled" section. Resolution now includes the index aliases, so a container
whose image is named after an alias is detected too.

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
- [Configuration file](../reference/config-file.md) — the rest of `dumbdock.json`.
- [API](../reference/api.md) — the `/api/icons` payload behind the Icons tab.
- [Network warnings](./networking.md) — the other badges on a card.
