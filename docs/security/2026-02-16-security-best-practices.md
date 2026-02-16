# Security Best Practices Report

Date: 2026-02-15
Scope: `.`
Reviewer mode: Active security audit (Go backend + browser JS frontend)

## Executive Summary
The codebase already has a strong baseline in several areas: Argon2id password hashing, secure session cookie defaults (`HttpOnly`, `SameSite`, `Secure` in production), CSRF middleware, SSRF-safe webhook transport, and explicit HTTP server timeouts. Automated dependency scans were also clean (`govulncheck` and `npm audit` both reported no known vulnerabilities).

I found 6 security issues that should be addressed for secure-by-default posture:
- 2 High
- 3 Medium
- 1 Low

The most important issues are a DOM-XSS sink in the Dify embed widget and upload validation that trusts client-provided MIME metadata, which can allow active content hosting under the app origin.

## High Severity

### [SEC-001] DOM XSS in Dify widget bot-message rendering
- Rule ID: `JS-XSS-001`
- Severity: High
- Location:
  - `modules/embed/providers/dify.go:336`
  - `modules/embed/providers/dify.go:395`
  - `modules/embed/providers/dify.go:344`
- Evidence:
  - Bot output is rendered using `innerHTML`:
    - `d.innerHTML=type==='bot'?fmt(t):esc(t);`
    - `botMsg.innerHTML=fmt(full);`
  - `fmt()` applies markdown-like replacements but does not HTML-escape input first.
- Impact:
  - If the model output contains HTML with executable attributes (for example `<img onerror=...>`), script can execute in the site origin when the widget is enabled.
- Fix:
  - Treat bot output as untrusted.
  - Escape before formatting, or use a sanitizer with strict allowlist (for example DOMPurify with limited tags/attributes).
  - Prefer `textContent` and render markdown with a sanitizer-enabled parser.
- Mitigation:
  - Add Trusted Types enforcement and tighten CSP where feasible.
- False positive notes:
  - Exploitability depends on embed module use, but LLM output must be considered untrusted by default.

### [SEC-002] File upload type validation trusts client metadata, enabling active content upload under app origin
- Rule ID: `GO-HTTP-002`
- Severity: High
- Location:
  - `internal/service/media.go:77`
  - `internal/service/media.go:80`
  - `internal/service/media.go:82`
  - `internal/service/media.go:320`
  - `cmd/ocms/main.go:1022`
- Evidence:
  - MIME allow decision uses multipart header `Content-Type` (client-controlled), falling back to extension.
  - Filename sanitization preserves arbitrary extension.
  - Uploaded files are served directly from `/uploads/*` under the primary origin.
- Impact:
  - A user with upload capability (including API key with `media:write`) can store `.html`/`.js`/other active content by spoofing MIME headers, then serve it from the same origin. This can be used for phishing, script execution flows, and privilege escalation chains.
- Fix:
  - Detect real content type server-side (`http.DetectContentType` on file bytes) and enforce strict extension↔MIME mapping.
  - Rewrite stored extension from detected type rather than trusting user filename.
  - For non-image documents, serve with `Content-Disposition: attachment` where possible.
  - Consider serving uploads from a separate cookieless domain.
- Mitigation:
  - Reject dangerous extensions regardless of MIME (`.html`, `.htm`, `.js`, `.svg`, `.xml`, etc.) unless explicitly required and sandboxed.
- False positive notes:
  - Requires upload permission, but this still violates secure-by-default for multi-user/admin contexts.

## Medium Severity

### [SEC-003] Missing explicit request-body size caps on multiple JSON and multipart handlers
- Rule ID: `GO-HTTP-002`
- Severity: Medium
- Location:
  - `internal/handler/media.go:268`
  - `internal/handler/importexport.go:224`
  - `internal/handler/api/media.go:313`
  - `internal/handler/api/media.go:545`
  - `internal/handler/forms.go:730`
- Evidence:
  - Handlers call `ParseMultipartForm` / `json.NewDecoder(r.Body)` without `http.MaxBytesReader` hard caps.
- Impact:
  - Large request bodies can consume excessive memory/disk and degrade availability.
- Fix:
  - Wrap body with `http.MaxBytesReader` before parsing/decoding.
  - Define per-route caps (for example small JSON limits vs controlled upload limits).
  - Add centralized middleware defaults for JSON APIs.
- Mitigation:
  - Enforce body limits at reverse proxy/ingress as defense in depth.
- False positive notes:
  - If edge limits are guaranteed in all deployments, practical risk is reduced; app-level limits are still recommended.

### [SEC-004] Login IP rate-limiting can be bypassed if proxy headers are user-controlled
- Rule ID: `GO-AUTH-001`
- Severity: Medium
- Location:
  - `internal/middleware/login_protection.go:251`
  - `internal/middleware/login_protection.go:274`
  - `internal/middleware/login_protection.go:276`
  - `internal/middleware/login_protection.go:285`
- Evidence:
  - Rate limit key is derived from `X-Forwarded-For` / `X-Real-IP` directly.
- Impact:
  - If the app is reachable without a trusted proxy sanitizing headers, attackers can spoof IPs and evade per-IP throttling/audit integrity.
- Fix:
  - Trust forwarding headers only from explicit trusted proxy ranges.
  - Otherwise use `RemoteAddr` exclusively.
  - Document required reverse-proxy header-stripping behavior.
- Mitigation:
  - Keep account-based lockout in place (already implemented).
- False positive notes:
  - Risk is deployment-dependent; lower when all traffic is forced through trusted edge infrastructure.

### [SEC-005] Dify API key is exposed to all site visitors in client-side JavaScript
- Rule ID: `GO-CONFIG-001`
- Severity: Medium
- Location:
  - `modules/embed/providers/dify.go:279`
  - `modules/embed/providers/dify.go:375`
  - `modules/embed/providers/dify.go:407`
- Evidence:
  - `KEY` is embedded in page JavaScript and used in browser `Authorization: Bearer` requests.
- Impact:
  - Visitors can extract and reuse the key, causing quota abuse, cost exposure, and potential data access based on key scope.
- Fix:
  - Move provider calls behind a backend proxy and keep upstream secret server-side.
  - If the provider supports it, issue short-lived scoped client tokens.
- Mitigation:
  - Restrict key scope, rate-limit upstream usage, rotate keys periodically.
- False positive notes:
  - If this is intentionally a non-secret public token with strict external controls, severity is lower.

## Low Severity

### [SEC-006] Logout is state-changing and allowed via GET
- Rule ID: `GO-CSRF-001`
- Severity: Low
- Location:
  - `cmd/ocms/main.go:702`
  - `internal/handler/auth.go:262`
  - `internal/handler/auth.go:273`
- Evidence:
  - `GET /logout` is routed and destroys session.
- Impact:
  - Cross-site requests can force user logout (logout CSRF).
- Fix:
  - Remove GET logout route and keep POST-only logout protected by CSRF.
  - Optional: GET can serve a confirmation page only.
- Mitigation:
  - SameSite cookies reduce some cross-site behavior, but do not justify state-changing GET.
- False positive notes:
  - Primarily availability/UX impact, not direct confidentiality/integrity compromise.

## Positive Findings
- Session cookie hardening present (`HttpOnly`, `SameSite`, production `Secure`, `__Host-` prefix): `internal/session/session.go:33`
- CSRF middleware integrated in auth/admin/form routes: `cmd/ocms/main.go:699`, `cmd/ocms/main.go:732`, `cmd/ocms/main.go:997`
- SSRF controls for webhook URLs and outbound delivery dialing: `internal/util/network.go:64`, `internal/webhook/delivery.go:37`
- HTTP server timeouts and header size limits set: `cmd/ocms/main.go:1114`
- Supply chain checks clean at review time:
  - `govulncheck`: `.audit/govulncheck-2026-02-15_15-46-29.log`
  - `npm audit`: `.audit/npm-audit-2026-02-15_15-46-29.log`

