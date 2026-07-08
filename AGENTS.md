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

## Security Issues (from security review, 2026-07-01)

### H-02: XSS via dumbdock.href label
Containers with `dumbdock.href: "javascript:alert(1)"` execute JavaScript in dashboard viewers' browsers.
**Fix:** Validate URL protocol in `index.html` — only allow `http:` and `https:`.

### H-03: Nil pointer dereference crash
If `loadConfig()` returns `nil, error` (e.g., unparseable JSON file), the server panics.
**Fix:** Assign fallback empty config after error: `cfg = &overrideConfig{Containers: map[string]cardOverride{}}`.

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

## README.md

* Always keep the README.md file up to date with the latest changes made to the codebase. If you make a change that requires an update to the README.md file, make sure to update it accordingly.

## Versioning

* The `VERSION` file at the repo root is the single source of truth for the semantic version string (e.g., `0.0.1`). It is embedded at compile time via `//go:embed VERSION`.
* The build number is derived from `git rev-list --count HEAD` at build time and injected via ldflags (`-X main.buildNumber=$(git rev-list --count HEAD)`). Local `go build` defaults the build number to `"0"`.
* To bump the version: edit `VERSION`, commit, tag with `v<version>` (e.g., `v0.0.1`), and build.
* The existing `version` ldflags variable (git SHA + timestamp) is kept unchanged for ETag / cache-busting purposes and is independent of the semantic version.
* Version info is exposed via:
  - `GET /api/version` endpoint returning `{"version":"<semver>","build":"<count>"}`
  - Dashboard UI footer showing `v<semver> (build <count>)`
  - Startup log line: `dumbdock v<semver> (build <count>)`