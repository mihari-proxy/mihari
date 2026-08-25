# Security Policy

[English](SECURITY.md) · [简体中文](SECURITY.zh-CN.md)

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | ✅ |
| < 1.0   | ❌ |

## Reporting a Vulnerability

**Do not report security vulnerabilities in public Issues.**

Please use GitHub Security Advisories for private reporting:

1. Visit [Security Advisories](https://github.com/mihari-proxy/mihari/security/advisories)
2. Click "Report a vulnerability"
3. Fill in the vulnerability details

### What to Include

- Vulnerability description
- Steps to reproduce
- Impact scope
- Possible fix suggestions (if any)

### Response Commitment

- **Acknowledgment**: Within 3 business days
- **Assessment**: Within 7 business days to evaluate severity
- **Fix**: Release a fix as soon as possible based on severity

### Disclosure Policy

- Vulnerability details will not be publicly disclosed without the reporter's consent
- After a fix is released, the reporter will be acknowledged in Release Notes (if desired)

## Security Model

mihari is designed with the goal of "local-only, not exposed to the network". Understanding the following invariants helps report accurate issues:

- **Control pipe never binds TCP**: The local control API uses Windows named pipe (`\\.\pipe\mihari-control`) or Unix domain socket, never listens on a TCP port.
- **Controller loopback-only**: mihomo controller only binds to loopback; browsers/CLI never obtain the controller address or secret.
- **Web gateway defaults to loopback**: `web-addr` defaults to `127.0.0.1:9191`; browser authentication uses a Web access credential independent of the controller secret, never printed to status/logs/stdout.
- **Unknown write operations rejected**: All REST/WebSocket write operations from Web panels go through a unified mutation coordinator; unknown writes are rejected by default; core upgrades and hosted field writes never reach mihomo directly.
- **Never overwrite others' proxy**: `sysproxy enable` fails by default when another proxy is present (`system_proxy_conflict`), requires `--force` to overwrite; `sysproxy disable` only clears mihari's own proxy.
- **Subscription URLs never leaked**: Subscription URLs are stored only in the daemon's private directory, not included in list/show responses or regular errors.

## Security Recommendations (User-side)

- Do not commit the data directory (containing `control.token`, subscription URLs) to public repositories or share with others
- Access Web panel from loopback by default; for remote access, configure a TLS reverse proxy instead of exposing the port directly
- When `sysproxy enable` conflicts, investigate the other product's proxy first instead of blindly using `--force`
- Keep updated to the latest version

## Scope

The following scenarios are **not** within the scope of security vulnerabilities in this repository (please use regular Issues for feedback):

- Security issues in the mihomo dependency itself (please report to [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo))
- Security issues in Web panels (zashboard, MetaCubeXD) themselves (please report to the respective upstreams)
- Local operations requiring administrator privileges being denied (this is a design constraint: mihari does not auto-elevate)
