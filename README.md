# mihari

Mihari is a new, independent local manager for mihomo. It targets Windows, Linux, and macOS equally and is designed around a single daemon-owned control plane shared by the CLI, TUI, and browser panels.

The current runtime slice provides an authenticated local daemon control API over a Windows named pipe or Unix domain socket. It can install, validate, supervise, query, and restart mihomo while keeping the controller on loopback. It also owns subscription persistence, bounded automatic refresh, validated config generation, reload rollback, and offline profile switching. Mihari's control API does not bind a TCP port.

## Current commands

Launch the interactive TUI (attached terminal only):

```console
mihari
```

Bare `mihari` starts the TUI only when stdin is an interactive terminal. Non-interactive or piped invocations reject the bare entry point so automation never accidentally enters full-screen mode. Explicit CLI subcommands always remain available and retain `--json` for machine-readable output.

The TUI talks only to the local daemon through `internal/control/client` over the native IPC control plane. It never opens the mihomo controller, never receives the controller secret, and never performs durable writes itself. Bracketed paste and Ctrl+V in search and form fields use the pure-Go `github.com/atotto/clipboard` helper; Mihari itself never writes secrets to the clipboard. The current TUI includes a standalone first-run Setup route, Overview, expandable Proxies, active/closed Connections with local GeoIP details, Rules/Providers, a bounded structured Logs stream, subscription management forms, a categorized System page, and a Web GUI page that drives panel install/update/activate/open/rollback once the daemon advertises the `web-gui` capability. Setup installs the core, can add an initial subscription, prepares local GeoIP data, and asks the daemon to persist validated local endpoints. The System page also manages the OS service (install/uninstall/start/stop/restart/status) via the same local service adapter as `mihari service`; those actions require an already-elevated process and do not go through the daemon control protocol. When the daemon advertises the capabilities, the System page shows live system-proxy and TUN status and toggles them through the local control API (foreign proxy enable asks for force confirmation; Mihari never clears another product's proxy). Rule order is never sorted; onboarding, system, provider, subscription, panel, and browser mutations run through the daemon mutation coordinator, and destructive or broad operations require confirmation.

Run the daemon in the foreground (debug / one-off):

```console
mihari daemon
```

Install as an OS service so it keeps running after you close the terminal (**Administrator / root required**; Mihari does **not** auto-elevate):

```console
# Windows: open "Terminal (Admin)" or elevated PowerShell first
mihari service install
mihari service start
mihari service status
mihari service stop
mihari service restart
mihari service uninstall
```

After `service install` + `start`, closing the TUI or a normal console does **not** stop Mihari; only `service stop`, uninstall, or the OS does. The same controls are available under the TUI **System** page (elevated shell required for mutations).

Update the mihari binary itself (also requires elevation):

```console
mihari self version
mihari self update
```

Query daemon status (works without admin once the service/daemon is up):

```console
mihari status
mihari status --json
```

Manage and inspect mihomo through the daemon:

```console
mihari core status
mihari core install
mihari core update
mihari core restart
mihari proxy groups
mihari proxy select GROUP PROXY
mihari proxy test GROUP
mihari connections list
mihari connections close ID
mihari connections close-all --yes
mihari rules list
mihari traffic --follow
mihari logs --follow
```

Manage subscriptions through the same daemon mutation coordinator:

```console
mihari sub add NAME URL
mihari sub list
mihari sub show ID
mihari sub refresh ID
mihari sub use ID
mihari sub enable ID
mihari sub disable ID
mihari sub set ID --interval 6h --auto-refresh=true
mihari sub remove ID --yes
```

Subscription URLs are stored only in the daemon-private catalog and are omitted from list/show responses and normal errors. Each valid profile has an independent cache, so `sub use` works without provider network access. Generated configuration always restores Mihari's managed loopback controller, secret, and port invariants before `mihomo -t` and reload.

Manage OS system proxy and managed TUN through the same daemon mutation path:

```console
mihari sysproxy status
mihari sysproxy enable
mihari sysproxy enable --force
mihari sysproxy disable
mihari tun status
mihari tun enable
mihari tun disable
```

`sysproxy enable` points the desktop HTTP/HTTPS/SOCKS system proxy at Mihari’s mixed endpoint. If another product already owns the proxy, enable fails with `system_proxy_conflict` unless you pass `--force` (TUI asks for confirmation). `sysproxy disable` only clears a proxy **owned by Mihari**; it will not turn off a foreign proxy. On Windows, when Mihari runs as a LocalSystem service it writes the **interactive console user’s** WinINET hive (`HKEY_USERS\<SID>\…`), not SYSTEM’s own `HKCU`, so desktop browsers pick up the change.

`tun enable|disable` persists a managed TUN block, injects it into generated mihomo config, and applies live via the controller when available. TUN may require elevated privileges or a service install depending on the OS.

Manage browser panels through the same daemon-owned lifecycle:

```console
mihari panel list
mihari panel install ID
mihari panel update ID
mihari panel use ID
mihari panel open
mihari panel rollback ID --yes
```

The daemon starts a loopback Web gateway on `web-addr` (default `127.0.0.1:9191`). Browser authentication uses a dedicated Web access credential stored under the data root; it is never the mihomo controller secret and never appears in status DTOs, default CLI output, or logs. `panel open` mints a one-shot local URL, launches the OS browser, and does not print the token. Panel static assets live under `web/{panel}/{build}/` with atomic `active.json` switching and one retained previous build for rollback. Browser REST and WebSocket traffic is authenticated at the gateway; the gateway injects the controller secret only into proxied controller requests. Unknown writes are rejected by default; core upgrade and managed-field writes never reach mihomo.

Supported first-release adapters: **Zashboard** (release dist zip, prefer no-fonts when available) and **MetaCubeXD** (`gh-pages` tree keyed by commit SHA). Default `go test ./...` uses fixtures only and does not contact the public network for panel downloads.

GeoIP connection details are resolved locally by the daemon. Country and ASN MMDB files are downloaded from the public `Loyalsoldier/geoip` release branch, verified against the matching `.sha256sum` files, validated as MMDB databases, and refreshed when either local file is missing or at least 30 days old. A failed refresh retains the previous valid database pair and does not disable other connection details.

Every explicit CLI command supports human-readable output and `--json`. `--json` emits a versioned success or error envelope and stable process exit codes for automation.

## Platform targets

- Windows amd64 and arm64
- Linux amd64 and arm64
- macOS amd64 and arm64

All release binaries are CGO-free.

## Development

```console
go test ./...
go test -race ./...
go vet ./...
```

The architecture and staged delivery scope are recorded in the [architecture design](docs/superpowers/specs/2026-08-03-mihari-architecture-design.md) and [delivery roadmap](docs/superpowers/plans/2026-08-03-mihari-delivery-roadmap.md).

Phase 4–5 are sealed. Phase 6 adds OS service install/control (Windows Service / systemd / launchd via `kardianos/service`), **no automatic privilege elevation** (commands fail until run as Administrator/root), mihari self-update from GitHub Releases, and CI cross-builds.
