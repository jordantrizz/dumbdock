# Security Review — dumbdock

**Review date:** 2026-07-01
**Scope:** All source files, Dockerfile, docker-compose.yml.example, README.md
**Application type:** Go web daemon polling the Docker socket for a container dashboard

---

## Summary

| Severity | Count | Notes |
|----------|-------|-------|
| 🔴 **Critical** | 1 | Docker socket exposure (inherent) |
| 🟠 **High** | 4 | XSS, crash DoS, memory exhaustion, no authentication |
| 🟡 **Medium** | 5 | Secret leakage, SSRF, supply chain, missing TLS |
| 🔵 **Low** | 7 | Missing hardening headers, protocol injection, rate limiting |
| ℹ️ **Info** | 3 | Version pinning, fingerprinting, code style |

**Total: 20 findings**

---

## 🔴 Critical

### C-01: Docker Socket Exposure (Inherent Risk)

| Attribute | Value |
|-----------|-------|
| **File(s)** | `docker.go`, `docker-compose.yml.example` |
| **Line(s)** | `docker.go:20-31`, `docker-compose.yml.example:18` |

**Description:** dumbdock requires access to the Docker daemon socket (`/var/run/docker.sock`) to function. While the socket is mounted read-only (`:ro`) in the recommended deployment, any vulnerability in dumbdock (RCE, SSRF, path traversal) could allow an attacker to interact with the Docker API. Even read-only access leaks significant information.

**Impact:** If the web dashboard or an API endpoint is compromised, an attacker can enumerate all running containers, their images, port mappings, labels, and network configuration — enabling reconnaissance for further attacks.

**Remediation:**
- Keep the socket mount read-only (`:ro`) — already done in `docker-compose.yml.example`, good.
- Restrict network access to dumbdock (bind only to `127.0.0.1` or use a firewall).
- Run dumbdock behind a reverse proxy for authentication (see H-01).
- Consider implementing a dedicated Docker API user with limited permissions (Docker's `security-opt` or OAuth-based access).
- Regularly update dumbdock and the host Docker daemon.

---

## 🟠 High

### H-01: No Authentication on Web Dashboard

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go`, `index.html` |
| **Line(s)** | `main.go:324-327` (HTTP server start) |

**Description:** All HTTP endpoints (`/api/containers`, `/api/icons`, `/api/version`, `/`) are publicly accessible with no authentication, authorization, or session management. The only access control is the network binding address.

**Impact:** Any entity with network access to the dashboard can:
- View all running containers with full metadata (image names, port bindings, labels, Traefik URLs)
- Discover internal hostnames and network topology via Traefik rules
- Access the icon index endpoint

**Remediation:**
1. Implement HTTP Basic Auth or Bearer token authentication as an optional feature (env var `AUTH_TOKEN` or `AUTH_USERNAME`/`AUTH_PASSWORD`).
2. Bind to `127.0.0.1` by default in code (not just in the compose example).
3. Document the expectation of running behind a reverse proxy (nginx, Caddy, Traefik) with auth.
4. Add a `TRUSTED_PROXY_CIDR` option if auth is delegated to a reverse proxy.

---

### H-02: Cross-Site Scripting (XSS) via `dumbdock.href` Label

| Attribute | Value |
|-----------|-------|
| **File(s)** | `index.html` |
| **Line(s)** | `index.html:314` (`href` attribute), `index.html:380-385` (escapeHtml) |

**Description:** The `escapeHtml()` function escapes `&`, `<`, `>`, and `"` but does **not** block dangerous URI schemes like `javascript:`. A container with the label:

```yaml
dumbdock.href: "javascript:alert(document.cookie)"
```

will render as:

```html
<a class="card-name" href="javascript:alert(document.cookie)" target="_blank" rel="noopener">...</a>
```

Since `rel="noopener"` does not prevent `javascript:` execution, clicking the link executes arbitrary JavaScript in the dashboard viewer's browser session. The same issue applies to icon URLs (`<img src="...">`) and Traefik URL links.

**Impact:** An attacker who can create a Docker container with a malicious `dumbdock.href` label can execute arbitrary JavaScript in the context of anyone viewing the dumbdock dashboard. This enables session hijacking, credential theft, internal network scanning from the victim's browser, and dashboard defacement.

**Remediation:**
1. **Validate the protocol** in JavaScript before inserting into `href` attributes — reject `javascript:`, `data:`, `vbscript:`, `file:`, etc.
2. **Apply a strict URL sanitizer** — only allow `http:` and `https:` schemes in rendered links.
3. As a defence-in-depth measure, add a `Content-Security-Policy` header (see L-01).
4. Consider also validating `href` values server-side in `main.go` or `labels.go`.

**Example sanitization for `href`:**

```javascript
function sanitizeUrl(url) {
  if (!url) return '';
  const allowed = /^https?:\/\//i;
  return allowed.test(url) ? url : '';
}
```

---

### H-03: Nil Pointer Dereference — Server Crash (DoS)

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go`, `config.go` |
| **Line(s)** | `main.go:49-52` (cfg assignment), `main.go:83` (applyOverrides call), `main.go:118` (isAutoDetectionEnabled call) |

**Description:** When the config file exists but is unreadable or contains invalid JSON, `loadConfig()` returns `(nil, error)`. The error is logged but `cfg` remains `nil`. Subsequent calls pass `cfg` to functions that dereference it without a nil check:

- `applyOverrides(cfg, unlabeled)` → accesses `cfg.Containers`
- `isAutoDetectionEnabled(cfg)` → accesses `cfg.AutoDetection`
- `autoDetectedGroupName(cfg)` → accesses `cfg.AutoDetectedGroup`
- `autoDetectionServiceBlacklist(cfg)` → accesses `cfg.AutoDetectionServiceBlacklist`
- `containerBlacklist(cfg)` → accesses `cfg.ContainerBlacklist`
- `initIconSets(cfg, iconHTTPClient)` → accesses `cfg.IconSets`

**Impact:** If the config file has a permission error or is corrupted, the server will panic on startup or on the first refresh cycle. This is a reliable denial-of-service vector: an attacker who can write an invalid JSON file to the config path, or cause a filesystem error, can crash the dashboard. Note that if the file does not exist, `loadConfig` returns a valid empty config — only read/permission errors and parse failures trigger the nil return.

**Remediation:**
In `main.go`, after the `loadConfig` call, assign a fallback empty config on error:

```go
cfg, err := loadConfig(configPath())
if err != nil {
    log.Printf("warning: config load: %v; using defaults", err)
    cfg = &overrideConfig{Containers: map[string]cardOverride{}}
}
```

Or, refactor `loadConfig` to never return nil:

```go
func loadConfig(path string) *overrideConfig {
    // ... always return a valid config
}
```

---

### H-04: Unbounded Memory Consumption from External Icon Index Fetching

| Attribute | Value |
|-----------|-------|
| **File(s)** | `icons.go` |
| **Line(s)** | `icons.go:178-180` (`io.ReadAll`) |

**Description:** The `readIndex()` function fetches icon index files from remote URLs using `io.ReadAll()` with no size limit on the response body. The default icon sets point to jsDelivr and GitHub raw content, but custom icon sets configured via `dumbdock.json` can point to arbitrary URLs. A malicious or compromised server could return an extremely large JSON payload, causing dumbdock to exhaust available memory.

**Impact:** An attacker who can configure the icon sets (either by writing to the config file or through a supply-chain compromise of the configured CDN/URL) can trigger a memory exhaustion denial of service. On resource-constrained deployments (e.g., Raspberry Pi), a few hundred MB of JSON could crash the process.

**Remediation:**
1. Add a response size limit using `http.MaxBytesReader` or `io.LimitReader`:

   ```go
   const maxIndexSize = 50 << 20 // 50 MB
   resp.Body = http.MaxBytesReader(w, resp.Body, maxIndexSize)
   body, err := io.ReadAll(resp.Body)
   ```

2. Set a reasonable timeout on the HTTP client used for icon fetches (already has 15s, but should be enforced per-request as well).
3. Consider validating index content by first checking `Content-Length` header if present.
4. Document the 50 MB limit for custom icon set providers.

---

## 🟡 Medium

### M-01: Docker Labels Exposed via API (Secret Leakage)

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go`, `labels.go` |
| **Line(s)** | `main.go:88-92`, `labels.go:41-42` |

**Description:** The `/api/containers` endpoint returns the complete `Labels` map for every container as part of the JSON response. Docker labels are commonly used to store metadata, but some users may inadvertently (or intentionally) store sensitive information such as database passwords, API keys, environment variables, or configuration details in container labels.

**Impact:** Anyone with access to the dashboard can read all container labels. If labels contain secrets, this represents a credential disclosure vulnerability.

**Remediation:**
1. **Option A (recommended):** Do not expose labels in the API response. The card data already captures the relevant `dumbdock.*` labels. The full label map is only shown in the "Unlabeled" expandable detail view for diagnostic purposes — consider filtering to only show `dumbdock.*`-prefixed labels in that view.
2. **Option B:** Add a configuration option to disable label exposure in the API (e.g., `EXPOSE_LABELS=false`).
3. **Worst case:** At minimum, document prominently that the API exposes all container labels and advise users not to store secrets in Docker labels.

---

### M-02: Gotify API Token in URL Query String

| Attribute | Value |
|-----------|-------|
| **File(s)** | `alerts.go` |
| **Line(s)** | `alerts.go:131` |

**Description:** The Gotify notification integration sends the API token as a URL query parameter:

```go
url := am.cfg.GotifyURL + "/message?token=" + am.cfg.GotifyToken
```

Passing authentication tokens in query strings is a well-known anti-pattern because:
- Query strings are logged by most HTTP proxies, load balancers, and web servers in plain text.
- Browsers may expose query strings in history, referrer headers, and bookmarks.
- Network-level packet captures record query strings in cleartext.

**Impact:** If the Gotify server is accessed through a proxy or reverse proxy that logs URLs, the token could be leaked. Similarly, network monitoring tools on the path could capture the token.

**Remediation:**
Use the `X-Gotify-Key` header instead, which is the officially supported method:

```go
req, err := http.NewRequest("POST", am.cfg.GotifyURL+"/message", body)
if err != nil {
    log.Printf("gotify alert failed: %v", err)
    return
}
req.Header.Set("Content-Type", "application/json")
req.Header.Set("X-Gotify-Key", am.cfg.GotifyToken)

resp, err := am.client.Do(req)
```

---

### M-03: No TLS / HTTPS Support

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go` |
| **Line(s)** | `main.go:327` (`ListenAndServe`) |

**Description:** The dashboard is served over plain HTTP with no built-in TLS support. All traffic, including API responses containing container metadata and Docker labels, is transmitted in cleartext.

**Impact:** On networks where dumbdock is not bound exclusively to localhost, an attacker with network access (same subnet, compromised router, ARP spoofing, etc.) can eavesdrop on all dashboard data, including container names, labels, port bindings, and internal network topology exposed via Traefik URLs.

**Remediation:**
1. Bind to `127.0.0.1` by default and rely on a reverse proxy (nginx, Caddy, Traefik) for TLS termination — this is already the recommended setup via the compose example.
2. Optionally add native HTTPS support using `ListenAndServeTLS` with configurable cert/key paths (`TLS_CERT_FILE`/`TLS_KEY_FILE` env vars).
3. Document the recommendation to never expose dumbdock's port directly to untrusted networks.

---

### M-04: Server-Side Request Forgery (SSRF) via Icon Set Configuration

| Attribute | Value |
|-----------|-------|
| **File(s)** | `icons.go` |
| **Line(s)** | `icons.go:152-186` (`readIndex`) |

**Description:** The `iconSets` configuration allows specifying arbitrary `indexUrl` values, which can be HTTP(S) URLs or local file paths. If an attacker can modify the config file (e.g., via container volume misconfiguration or a writeable mount), they can make dumbdock fetch resources from internal network hosts or read arbitrary files on the container filesystem.

The `readIndex()` function supports three URL schemes: `https://`, `http://`, `file://`, and raw absolute paths (`/path/to/file`). This means:
- An attacker could point `indexUrl` at `http://169.254.169.254/latest/meta-data/` to access cloud metadata services.
- An attacker could point `indexUrl` at `file:///etc/shadow` or `/etc/shadow` to read local files (though the output is just used for slug matching, error messages may leak content).

**Impact:** SSRF can enable cloud metadata service enumeration, internal network scanning, and local file reads from the dumbdock container.

**Remediation:**
1. **Restrict or remove** support for HTTP-to-localhost fetches. If only icon CDNs are intended, consider allowing only HTTPS and validating hosts against an allowlist.
2. If `file://` and local path support must be retained, add a config option `ALLOW_LOCAL_INDEX=true` that defaults to `false`.
3. Validate URL hosts — reject private IP ranges (RFC 1918, RFC 6598) and loopback addresses unless explicitly allowed.
4. Add a `MAX_INDEX_SIZE` limit (see H-04) to also limit SSRF payload bandwidth.

---

### M-05: External CDN Icon Loading — Supply Chain & Tracking Risk

| Attribute | Value |
|-----------|-------|
| **File(s)** | `icons.go`, `index.html` |
| **Line(s)** | `icons.go:86-94`, `index.html:313` |

**Description:** Icon images are loaded from external CDNs (jsDelivr for selfhst/icons and dashboard-icons) via `<img>` tags in the dashboard. This introduces two risks:

1. **Supply chain:** If the CDN is compromised or an icon repository maintainer pushes malicious content, SVG icons can contain JavaScript. While `<img>` tags block script execution in modern browsers, SVGs served directly or through redirects could potentially exploit browser vulnerabilities or leak information via HTTP headers during the fetch.

2. **Tracking:** The CDN provider can track which icons are loaded (i.e., which containers a dashboard is displaying), and by extension, the dashboard's IP address, User-Agent, and timing information.

**Impact:** Low in practice (modern browsers sandbox `<img>`-loaded SVGs), but represents unnecessary third-party dependency and privacy leakage.

**Remediation:**
1. Add an option to configure a local/self-hosted icon mirror or CDN (e.g., `ICON_CDN_BASE_URL` environment variable).
2. Consider embedding a curated set of SVG icons in the binary to eliminate external dependencies entirely.
3. At minimum, add `crossorigin="anonymous"` to `<img>` tags and document the external dependency so users can make an informed decision.
4. Add a `referrerpolicy="no-referrer"` attribute to icon `<img>` tags to prevent referrer leakage.

---

## 🔵 Low

### L-01: Missing Content-Security-Policy Header

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go` |
| **Line(s)** | `main.go:318-322` (root handler headers) |

**Description:** The `/` endpoint sets `Content-Type`, `Cache-Control`, and `ETag` headers but does not include a `Content-Security-Policy` header. Without CSP, if any XSS vulnerability exists (see H-02), an attacker has free rein to execute arbitrary scripts, make outbound requests, and exfiltrate data.

**Impact:** Defence-in-depth — CSP would mitigate the impact of the XSS vulnerability in H-02 by restricting script sources and preventing inline script execution.

**Remediation:**

Add a `Content-Security-Policy` header to the HTML response:

```go
w.Header().Set("Content-Security-Policy",
    "default-src 'self'; "+
    "img-src 'self' https://cdn.jsdelivr.net https://raw.githubusercontent.com; "+
    "style-src 'self' 'unsafe-inline'; "+
    "script-src 'self'; "+
    "connect-src 'self'; "+
    "form-action 'none'; "+
    "frame-ancestors 'none';")
```

**Note:** The `style-src 'unsafe-inline'` is required because styles are inlined in `index.html`. Consider moving styles to a separate file or using a CSP nonce to avoid `'unsafe-inline'`.

---

### L-02: Missing `X-Content-Type-Options: nosniff` Header

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go` |
| **Line(s)** | `main.go:318-322` (root handler), `main.go:306` (API handler) |

**Description:** None of the HTTP responses set `X-Content-Type-Options: nosniff`. Without this header, older browsers may perform MIME-type sniffing, which can lead to content-type confusion attacks.

**Impact:** Low — mainly a hardening best practice. In combination with other vulnerabilities, it could lower the bar for exploitation in legacy browsers.

**Remediation:**

Add the header to all responses. Consider using a shared middleware or a helper function:

```go
func setSecurityHeaders(w http.ResponseWriter) {
    w.Header().Set("X-Content-Type-Options", "nosniff")
    w.Header().Set("X-Frame-Options", "DENY")
    w.Header().Set("Referrer-Policy", "no-referrer")
}
```

---

### L-03: Missing `X-Frame-Options` Header

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go` |
| **Line(s)** | `main.go:318-322` |

**Description:** The dashboard does not set `X-Frame-Options`, allowing it to be embedded in `<iframe>`, `<frame>`, or `<object>` elements on other websites.

**Impact:** An attacker could frame the dumbdock dashboard in a malicious page (clickjacking). While authentication is not currently enforced, an attacker could trick a user into clicking dashboard elements. If authentication is added in the future (see H-01), this becomes a real clickjacking vector.

**Remediation:**

Set `X-Frame-Options: DENY` (or `SAMEORIGIN` if framing is intentionally supported).

---

### L-04: No Rate Limiting on API Endpoints

| Attribute | Value |
|-----------|-------|
| **File(s)** | `main.go` |
| **Line(s)** | `main.go:302-327` |

**Description:** The `/api/containers`, `/api/icons`, and `/api/version` endpoints have no rate limiting or request throttling. While this is a read-only dashboard, an attacker could abuse the API to:
- Poll aggressively, consuming Docker API resources (each request triggers a Docker API call).
- Perform recon at high speed.
- Amplify traffic in a DDoS scenario.

**Impact:** Low in a single-user dashboard context, but could degrade Docker daemon performance under sustained heavy polling.

**Remediation:**

Add simple rate limiting middleware — e.g., a token bucket or a per-IP rate limiter. For a simple approach:

```go
var rateLimiter = rate.NewLimiter(rate.Every(100*time.Millisecond), 10)

func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !rateLimiter.Allow() {
            http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

### L-05: Missing CSRF Protection

| Attribute | Value |
|-----------|-------|
| **File(s)** | `index.html` |

**Description:** The dashboard is read-only (no POST/PUT/DELETE endpoints), so there is no state-changing action to forge. However, the absence of SameSite cookie handling or CSRF tokens means that if write endpoints are ever added, CSRF protection would need to be retrofitted.

**Impact:** Currently none — purely a forward-looking finding.

**Remediation:**
- No action needed now, but any future addition of write endpoints should include CSRF tokens or enforce SameSite cookies.
- Add `SameSite` cookie attributes if session cookies are introduced.

---

### L-06: Icon URL in `<img src>` Allows Non-HTTP(S) Protocols

| Attribute | Value |
|-----------|-------|
| **File(s)** | `index.html` |
| **Line(s)** | `index.html:313` |

**Description:** The `dumbdock.icon` label value is inserted directly into an `<img src>` attribute. While modern browsers do not execute `javascript:` URLs in `<img src>`, older browsers may attempt to, and `data:` URIs can produce unexpected results. Additionally, an attacker could use `file://` URIs in icon URLs to probe internal path existence (though img src is sandboxed).

```javascript
const icon = card.icon ? '<img src="' + escapeHtml(card.icon) + '" alt="">' : '...';
```

**Impact:** Low in modern browsers. Some older or embedded browsers may attempt to resolve javascript: in img src, though this is generally blocked.

**Remediation:**

Validate icon URLs to only allow `http:` and `https:` schemes:

```javascript
function sanitizeIconUrl(url) {
    if (!url) return '';
    if (!url.startsWith('http://') && !url.startsWith('https://')) return '';
    return url;
}
```

---

### L-07: Traefik URLs from Labels Rendered as Clickable Links

| Attribute | Value |
|-----------|-------|
| **File(s)** | `index.html`, `warnings.go` |
| **Line(s)** | `index.html:333-336`, `warnings.go:39-44` |

**Description:** Traefik hostnames are extracted from container labels and rendered as clickable `https://` links. While the URLs are always prefixed with `https://` (hardcoded in `parseTraefikLabels` in `warnings.go`), an attacker who controls a container's `traefik.http.routers.*.rule` labels can specify arbitrary hostnames that link to phishing sites or malicious content.

**Impact:** A crafted hostname could trick a user into visiting an attacker-controlled site that looks legitimate. The `rel="noopener"` attribute mitigates window opener attacks but not phishing.

**Remediation:**

1. Add a `rel="nofollow noopener noreferrer"` to Traefik URL links.
2. Consider showing the hostname as plain text with a "copy" action instead of as a clickable link, since these are typically hostnames on the internal network, not public URLs.
3. Validate Traefik hostnames against a configurable allowed domain suffix (e.g., `*.example.com`).

---

## ℹ️ Info

### I-01: Hardcoded Docker API Version

| Attribute | Value |
|-----------|-------|
| **File(s)** | `docker.go` |
| **Line(s)** | `docker.go:32` |

**Description:** The Docker API version is hardcoded as `v1.45`. If the Docker daemon is upgraded or downgraded to a version that does not support this API version, the dashboard will fail.

**Impact:** None currently — this is a maintainability concern. The version is recent and widely supported.

**Remediation:**
- Use the Docker SDK for Go (`github.com/docker/docker/client`) instead of calling the raw API, which handles version negotiation automatically.
- Or negotiate the API version at startup via `GET /version` from the Docker socket.

---

### I-02: Static User-Agent String Fingerprints the Tool

| Attribute | Value |
|-----------|-------|
| **File(s)** | `icons.go` |
| **Line(s)** | `icons.go:174` |

**Description:** All HTTP requests to external icon index servers use a static User-Agent string: `"dumbdock/1.0"`. This identifies the tool to CDN operators and could be used for fingerprinting or blocking.

**Impact:** None. Included for awareness — some users may prefer a more generic User-Agent for privacy.

**Remediation:**
- Make the User-Agent configurable via an environment variable.
- Or use a generic User-Agent like `"Go-http-client/2.0"` (the Go default) for privacy-conscious deployments.

---

### I-03: Inline Event Handlers in `index.html`

| Attribute | Value |
|-----------|-------|
| **File(s)** | `index.html` |
| **Line(s)** | Throughout (e.g., `onclick`, `oninput`) |

**Description:** The dashboard JavaScript uses inline event handler attributes (`onclick="..."`, `oninput="..."`) rather than `addEventListener` in a `<script>` block. This is a code quality and CSP concern — if a strict CSP is ever applied (see L-01), `'unsafe-inline'` would be required for inline handlers unless they are refactored.

**Impact:** None currently. Refactoring to `addEventListener` would improve CSP compatibility and align with modern JavaScript best practices.

**Remediation:**
- Replace inline `onclick` attributes with `addEventListener('click', ...)` calls in the bundled JavaScript.
- The trade-off is slightly more complex code (event delegation or ID-based lookup), but it enables a stricter CSP.

---

## ✅ Positive Observations

Not everything is a finding — dumbdock does several things right from a security perspective:

1. **Read-only Docker socket mount** (`docker-compose.yml.example:18`). The compose example uses `:ro` to mount the Docker socket as read-only, preventing container creation/deletion abuse.

2. **`escapeHtml()` in JavaScript** (`index.html:380-385`). The rendering code properly escapes `&`, `<`, `>`, and `"` for all label values displayed in the dashboard, preventing the most common XSS vectors.

3. **`rel="noopener"` on external links** (`index.html:314`). All external links (card hrefs, Traefik URLs) include `rel="noopener"`, preventing the linked page from accessing `window.opener`.

4. **Scratch-based Docker image** (`Dockerfile:6-7`). The production image runs from `scratch` with only the static binary and CA certificates — no shell, no package manager, no unnecessary utilities. This minimizes the attack surface significantly.

5. **No dynamic HTML generation on the server.** All HTML is either embedded via `//go:embed` (static `index.html`) or served as JSON API responses. There are no server-side template injections.

6. **No write endpoints.** The API is entirely read-only (GET only), eliminating CSRF, SSRF-to-write, and injection attack surfaces on the server side.

7. **Configurable listen address.** The `LISTEN_ADDR` env var allows operators to bind to specific interfaces (default `:8080` could be tightened to `127.0.0.1:8080`).

8. **Configurable blacklists.** `CONTAINER_BLACKLIST` and `AUTO_DETECTION_SERVICE_BLACKLIST` give operators control over which containers and names are processed.

---

## Quick Remediation Checklist

| Priority | Action | Effort |
|----------|--------|--------|
| 🔴 | Keep `:ro` on Docker socket mount (already done) | None |
| 🟠 | Fix nil pointer dereference in `main.go` after `loadConfig` error | 5 min |
| 🟠 | Sanitize URL protocols (`javascript:`) in `index.html` | 15 min |
| 🟠 | Add `http.MaxBytesReader` limit in `readIndex()` | 10 min |
| 🟠 | Add optional auth (Basic Auth or Bearer token) | 1-2 hours |
| 🟡 | Remove Gotify token from query string → use `X-Gotify-Key` header | 10 min |
| 🟡 | Filter full Docker labels from API response | 30 min |
| 🟡 | Add CSP, `X-Content-Type-Options`, `X-Frame-Options` headers | 15 min |
| 🟡 | Restrict SSRF in icon index URLs (block private IPs / local paths) | 30 min |
| 🟡 | Document external CDN dependency | 5 min |
| 🔵 | Add rate limiting middleware | 15 min |
| 🔵 | Sanitize icon URLs in JavaScript | 10 min |
| ℹ️ | Negotiate Docker API version | 30 min |

---

*Security review conducted against commit on `main` branch. Findings reflect the state of the codebase as of 2026-07-01.*
