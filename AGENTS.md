# AGENTS.md

## Development Workflow

* Always create a git commit message for each change made, use the format feat, fix, docs, style, refactor, perf, test, chore after any file changes.
* git commit and push all changes made using the git commit message generted from the previous step.
* Print out the git commit message when completing any code work on the codebase.
* If you produce more than one commit-worthy change in a session, run `git commit` and `git push` for each completed change instead of only printing proposed messages. Print the message after each commit so the recorded history and the reported message stay aligned.
* Check docs-agent directory for reference material for manuals and other documentation for command commands and infrastructure or apis.
* Print a status report on each session summarizing what was changed and why
* Search `doc/` folder before creating new documentation files to avoid duplicates.
* After completing code work, suggest testing methods for the change to complete.
* Add any issues that might be useful for the future to AGENTS.md
* Go is not installed on the host. Run build/vet/test inside the local `golang:1.22-alpine` image:
  `docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine sh -c 'go build ./... && go vet ./... && go test ./...'`

## Security Issues (from security review, 2026-07-01)

### H-02: XSS via dumbdock.href label
Containers with `dumbdock.href: "javascript:alert(1)"` execute JavaScript in dashboard viewers' browsers.
**Fix:** Validate URL protocol in `index.html` — only allow `http:` and `https:`.

### H-03: Nil pointer dereference crash
If `loadConfig()` returns `nil, error` (e.g., unparseable JSON file), the server panics.
**Fix:** Assign fallback empty config after error: `cfg = &overrideConfig{Containers: map[string]cardOverride{}}`.
Implemented 2026-09-23 in `main.go` (`cfg` fallback after `loadConfig` error); `validateConfig` in `startup.go` reports the load error as a warning.

### H-04: Unbounded memory from icon index fetch
`readIndex()` uses `io.ReadAll` with no size limit — could exhaust memory.
**Fix:** Add `http.MaxBytesReader` or `io.LimitReader` in `icons.go`.

### M-02: Gotify token in URL query string
Token is passed as `?token=...` in URL — logged by proxies.
**Fix:** Use `X-Gotify-Key` header instead.

### M-01: All Docker labels exposed via API
Full label map returned in `/api/containers` — may contain secrets.
**Fix:** Filter to `dumbdock.*` labels only in API response.

### L-01 through L-03: Missing security headers
No CSP, X-Content-Type-Options, or X-Frame-Options on responses.
**Fix:** Add middleware to set security headers on all responses.

### Future: Authentication
No auth on dashboard — rely on reverse proxy or add Basic/Bearer auth.
Implemented 2026-09-04 (`DUMBDOCK_AUTH_MODE`: `none` / `http-auth` / `web-auth`; see README "Authentication"):
session cookie `dumbdock_session` uses `HttpOnly`, `SameSite=Lax`, `Path=/`, no `Secure` flag (terminate TLS in proxy);
sessions in-memory (8h, 30d remember-me, hourly purge); password compare is constant-time (SHA-256 + `crypto/subtle`).
The mode is also settable via `authMode` in `dumbdock.json`; the env var wins (`resolveAuthMode` in `config.go`).
When no mode is set it defaults to `web-auth` (reverted from `http-auth` on 2026-09-23); `none` is an explicit opt-out.
When an auth mode is active but `DUMBDOCK_PASSWORD` is empty, `generateAuthPassword` (in `auth.go`) creates a random
password for the run and `main.go` logs it once — an intentional exception to the redaction rule below; it is
in-memory only and rotates on every restart.
No new findings.

## README.md

* Always keep the README.md file up to date with the latest changes made to the codebase. If you make a change that requires an update to the README.md file, make sure to update it accordingly.

## Docker Compose Ports

* In `docker-compose.yml.example`, `DUMBDOCK_PORT` selects the **published host port only**
  (`127.0.0.1:${DUMBDOCK_PORT:-8080}:8080`); the app always listens on container port `8080`.
* **Never** pass `DUMBDOCK_PORT` into the container environment. Doing so makes the app listen on
  a different port than the published container port, so the host mapping answers nothing and the
  browser gets "connection refused". Use `LISTEN_ADDR` to change the in-container listen address.
* `control.sh`'s `ensure_free_dumbdock_port` bumps `DUMBDOCK_PORT` in `.env` to find a free **host**
  port; this only works because the container port stays fixed at `8080`.

## Container Healthcheck

Implemented 2026-09-23 (Dockerfile, `docker-compose.yml`, `docker-compose.yml.example`; see README "Container Healthcheck"):

* The runtime image is `FROM alpine:3.20` (not `scratch`) so busybox `nc` is available for the
  healthcheck. CA certs are installed with `RUN apk add --no-cache ca-certificates` (needed by the
  outbound registry checks in `updates.go`); do not revert to `scratch` without replacing the probe.
* The Compose `healthcheck` does a **pure TCP connect** to `127.0.0.1:8080` (`nc -z -w 2`) —
  `127.0.0.1` avoids DNS and `8080` is the fixed in-container port. It deliberately issues **no
  HTTP request**, so it never depends on a route, a status code, or the auth mode, and it does not
  require `/api/version` (or anything else) to be public. Do not switch it back to a `wget`/HTTP probe.
* Timing is `interval: 30s`, `timeout: 5s`, `retries: 3`, `start_period: 10s`.
* **Keep both compose files in sync** (`.yml` and `.yml.example`). `docker-compose.yml` is
  gitignored in this repo; only the example is committed, and `control.sh setup` copies it.
* The healthcheck is Compose-only (no Dockerfile `HEALTHCHECK`) and runs as the base image's
  default user; adding a non-root `USER` is a separate decision.

## Versioning

* The `VERSION` file at the repo root is the single source of truth for the semantic version string (e.g., `0.0.1`). It is embedded at compile time via `//go:embed VERSION`.
* The build number is derived from `git rev-parse --short HEAD` (short commit SHA) at build time and injected via ldflags (`-X main.buildNumber=$(git rev-parse --short HEAD)`). Local `go build` defaults the build number to `"0"`.
* To bump the version: edit `VERSION`, commit, tag with `v<version>` (e.g., `v0.0.1`), and build.
* The existing `version` ldflags variable (git SHA + timestamp) is kept unchanged for ETag / cache-busting purposes and is independent of the semantic version.
* Version info is exposed via:
  - `GET /api/version` endpoint returning `{"version":"<semver>","build":"<sha>"}`
  - Dashboard UI footer showing `v<semver> (build <sha>)`
  - Startup log line: `dumbdock v<semver> (build <sha>)`

## Startup Logging

Implemented 2026-09-23 (`startup.go`, `startup_test.go`; see README "Startup Logging"):

* `validateConfig` (in `startup.go`) runs once at startup and returns a `configWarning` for every
  invalid setting. Validation is **advisory**: warnings are logged as `warning: config: <field>: …`
  and the process continues with defaults — never exit on invalid config.
* Invalid raw values that resolve with a silent fallback (durations, port) are read from the
  environment in `validateConfig` so they are still reported.
* `logStartupConfig` prints the effective configuration as a `startup config:` block. **Secrets
  must be redacted** via `redactSecret` — never log `DUMBDOCK_PASSWORD`, `GOTIFY_TOKEN`,
  `TRAEFIK_API_TOKEN`, `TRAEFIK_API_PASS`, or config-file `traefikAPIToken`. Report presence only
  (`(set)` / `(not set)`). **Exception:** the random password generated by `generateAuthPassword`
  when `DUMBDOCK_PASSWORD` is empty is deliberately logged once at startup so the operator can
  sign in — do not extend this exception to any other secret.
* **Any new setting must be added to `startupConfig` and `logStartupConfig`**, and any new
  validation rule to `validateConfig` with a matching case in `TestValidateConfig`.
* Keep the documented `dumbdock v<semver> (build <sha>)` startup line; the `startup config:`
  block is additive.

## Image Update Checks

Implemented 2026-09-17 (`updates.go`, `updates_test.go`; see README "Image Update Checks"):

* The checker is the only component that makes **outbound** network calls to third parties
  (Docker Hub + GHCR registry APIs). Keep it off the request path: registry I/O runs in its own
  goroutine and `refresh()` only reads the TTL cache.
* Supported registries are Docker Hub (`registry-1.docker.io`) and GHCR (`ghcr.io`), anonymous
  only. Never send credentials, and never log registry tokens. Other registries / local-only
  images are reported as `unknown`, not errors.
* Follow the existing security rules for fetches: HTTP client timeouts, `io.LimitReader` on
  response bodies (see H-04), and bounded concurrency (`updateMaxConcurrency`).
* Toggle with `DUMBDOCK_UPDATE_CHECK` (default `true`) / config `updateCheck`; cache TTL via
  `DUMBDOCK_UPDATE_INTERVAL` (default `6h`). Failed checks retry after 15 minutes.
* `parseImageRef` in `updates.go` is the canonical image-reference normalizer for this feature;
  `imagePath` in `icons.go` remains the icon-specific parser.
* No new Go module dependencies: the registry client is stdlib-only (`net/http`, `encoding/json`).

## Traefik API Resilience

Implemented 2026-09-23 (`traefik.go`, `traefik_test.go`; see README "Traefik Dashboard"):

* **Reachability is distinct from detection.** `traefikContainerFound` means a running container
  matched; `traefikDataErr` is set to `API unreachable at <url>: <cause>` only when **all** API
  endpoints fail (`allEndpointFetchesFailed`). Partial failures leave `traefikDataErr` empty.
* **404s are "not configured", not errors.** `fetchTraefikEndpoint` wraps 404s with
  `errTraefikNotFound`; `fetchTraefikDashboard` collects them in `NotConfigured` (not
  `EndpointErrors`), so they never count toward `allEndpointFetchesFailed`, never appear in the
  partial-error banner, and render as "Not configured" (e.g. TLS certs). Keep it that way.
* **Support both Traefik v2 and v3 overview shapes.** `/api/overview` counts use
  `traefikOverviewCount`, which unmarshals a bare integer (v2) or an object with `total` (v3). Do
  not revert it to `int` — that breaks v3 with `cannot unmarshal object into … of type int`.
* **dumbdock requires Traefik v3.** `parseTraefikMajorVersion` extracts the major version from
  `/api/version`; `MajorVersion` / `VersionSupported` are exposed in the payload. A non-v3 (or
  unparseable) version is an **informational error**, not `traefikDataErr`: it renders as the
  "Unsupported Traefik version" banner, logs once per state change via the folded
  `traefikLogSignature` (version marker + error signature), and must never hard-block the
  dashboard or be reported as `API unreachable`. `traefikSupportedMajorVersion` is the single
  source of the supported major (3); bump it only with a deliberate compatibility decision.
* **Log only on state change.** Endpoint errors and the unreachable warning are logged once per
  error-signature change (`traefikErrorSignature`, deterministic across map order), with a single
  `traefik: API recovered at <url>` on transition back to healthy. Do not log a success
  `detected …` line while all endpoints are failing, and never on every poll.
* **Fallback probing is bounded and unauthenticated.** When all endpoints fail, `traefikCandidateURLs`
  yields ordered, de-duplicated candidates (primary, published `8080/tcp`, container IP `:8081`,
  other published TCP ports); `probeTraefikAPIURL` does a 2s `GET /api/version` with no credentials
  and counts any HTTP response (incl. 401/403) as reachable. At most one probe round per poll.
* **Frontend contract.** `index.html` treats `endpointErrors` covering all 9 endpoints with no data
  as the strong "API is not accessible" state and shows the URL + error text; partial errors list
  `key: message` pairs. All interpolated values stay `escapeHtml`-escaped (H-02).
* No new settings: `startupConfig` / `logStartupConfig` / `validateConfig` are unchanged.

## Help Tab

Implemented 2026-09-23 (`index.html`; see README "Help Tab"):

* The **Help** tab is always visible, immediately after Traefik. It is pure static markup in
  `#page-help`/`#help-app` — no `loadHelp()` fetch, no `setInterval` refresh, and no backend route.
  `setActiveTab('help')` only toggles visibility + nav highlight; `switchTab('help')` additionally
  syncs the URL fragment (see "Tab Persistence" below). Keep the no-fetch rule.
* Add future guides by appending a `.traefik-section` block inside `#page-help` and an entry in the
  guide table of contents (`.help-toc`). Reuse the existing `traefik-*`/`help-*` classes.
* Help content is authored as static HTML only — never interpolate user/container data into it
  (no XSS surface, per H-02).

## Tab Persistence

Implemented 2026-09-23 (`index.html`; see README "Tab Persistence"):

* The active tab is stored in the URL fragment as a bare `#<tab>` id (`dashboard`, `networking`,
  `icons`, `traefik`, `help`). Clicking a tab pushes a history entry (`location.hash = tab`), so
  refresh (Ctrl+R), bookmarks, and back/forward all restore the tab.
* `tabFromHash(hash, doc)` resolves a fragment: a known tab id maps directly; any other fragment
  maps to the tab whose `page-*` element contains the matching element id (this is how the in-page
  `#help-traefik-api` / `#help-traefik-verify` anchors keep working); anything else falls back to
  `dashboard`. The fragment is only used for element lookups — **never interpolate it into the
  DOM** (H-02).
* `setActiveTab(tab)` only toggles `page-*` visibility and the `nav-<tab>` highlight (and sets
  `currentTab`); `switchTab(tab)` calls it, then `refreshActiveTab()`, then syncs `location.hash`.
  Keep the split: the initial load calls `setActiveTab(tabFromHash(location.hash))` for visibility
  only, and the active tab's data is fetched by `refreshActiveTab()` after `checkAuth()` resolves,
  so no request is made before auth.
* The `hashchange` listener returns early when the resolved tab already equals `currentTab`, so
  `switchTab`'s own hash assignment does not double-load. The 10s `setInterval` refresh is
  unchanged (it keys off the `page-*` display state).
* Nav buttons must keep their `id="nav-<tab>"` (the code selects by id, not position — this also
  fixed the old Icons→Networking highlight bug). Update both README "Tab Persistence" and this
  note when adding or renaming a tab.


