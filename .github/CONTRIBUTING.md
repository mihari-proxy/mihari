# Contributing to mihari

[English](CONTRIBUTING.md) · [简体中文](CONTRIBUTING.zh-CN.md)

Thank you for considering contributing to mihari!

## Development Environment

### Requirements

- Go 1.26.5 (the language version in `go.mod` is 1.26.0, and the toolchain is pinned to `go1.26.5` using `toolchain go1.26.5`; both CI and release workflows select this version via `go-version-file: go.mod`)
- Git

### Fork and Clone

```sh
# After forking the repository
git clone https://github.com/<your-username>/mihari.git
cd mihari
```

### Build

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/mihari ./cmd/mihari
```

Release builds additionally inject the version:

```sh
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/mihari-proxy/mihari/internal/buildinfo.Version=<tag>" -o bin/mihari ./cmd/mihari
```

Release builds must use both `-buildvcs=false` and `-trimpath`. The former prevents Go from writing different VCS/module metadata to the same commit before and after tag creation, while the latter removes local build paths; version identity is injected only via `buildinfo.Version` above.

The all-in-one release inputs are fixed in `scripts/release-inputs.lock.json` within the repository. The release workflow only consumes this file and does not query mihomo's latest release or GeoIP's mutable branches during release. When upstream inputs need updating, maintainers should run in a separate release-prep PR:

```sh
go run ./scripts/resolve-release-inputs --channel stable --out scripts/release-inputs.lock.json
```

The resolver validates and locks precise mihomo release/assets and GeoIP commit/digests; when affected by GitHub API rate limits, credentials can be provided via the `GITHUB_TOKEN` environment variable. After generation, the lock diff must be reviewed and only merged after PR validation passes. Do not run the resolver in the release workflow.

### Run Tests

```sh
go test ./...
go test -race ./...
```

> By default, `go test ./...` only uses fixtures and does not access the public internet (panel download tests are automatically skipped).

## Code Standards

### Formatting

Ensure code passes `gofmt` checks:

```sh
gofmt -l .
# No output means formatting is correct
```

> On Windows with `core.autocrlf=true`, `gofmt -l` may falsely report unformatted files due to CRLF. CI (which checks out LF) is the source of truth.

### Static Analysis

```sh
go vet ./...
```

### Lint (Consistent with CI)

CI's `lint` job uses golangci-lint v2 (see `.golangci.yml`). Install and run locally:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
golangci-lint run ./...
```

> The version must match CI (`ci.yml` pins `version: v2.12.2`), because the v1 line is built on go1.24 and cannot load this project's go1.26 configuration.

### Code Style

- Follow [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- Add comments for exported functions/types
- Do not ignore error handling
- Platform-specific implementations should be isolated through small interfaces and files like `_windows.go`, `_unix.go`, `_linux.go`, `_darwin.go`; platform branches should not be scattered in common files

## Architecture Constraints (Inviolable)

mihari's architectural invariants are recorded in `AGENTS.md` in the repository root. Key points:

- The daemon is the sole owner and writer of persistent state and the mihomo lifecycle; CLI/TUI only access the daemon through `internal/control/client` and the versioned local control protocol
- The local control API uses Windows named pipe or Unix domain socket, **must not** degrade to TCP listening
- mihomo controller only binds to loopback; browsers must not obtain the controller address or secret
- The `/v1` DTOs, error codes, JSON envelope, and CLI exit codes in `internal/control/protocol` are stable contracts; semantic breaking changes require a new protocol version
- Release builds maintain `CGO_ENABLED=0`

Before changing these boundaries, please explain the impact in an Issue/PR first.

## Commit Guidelines

### Commit Message Format

```
<type>: <short description>

[optional detailed description]
```

Type examples:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation update
- `refactor`: Refactoring
- `test`: Test-related
- `chore`: Build/tool-related

Example:
```
feat(control): add subscription refresh endpoint

Persist refresh errors in the catalog so `sub show` can surface
the last failure reason without exposing subscription URLs.
```

### Commit Requirements

- One commit per issue
- Use English for commit messages
- Ensure each commit passes tests and builds

### DCO Sign-off

This project requires every commit to include a **Developer Certificate of Origin (DCO)** sign-off. The `dco` workflow in CI will verify this.

Add the `-s` flag when committing to sign automatically:

```sh
git commit -s -m "feat: your feature description"
```

This automatically adds the sign-off:

```
Signed-off-by: Your Name <your.email@example.com>
```

#### Local commit hook (recommended)

The repository includes a `.githooks/commit-msg` hook: when a commit message lacks `Signed-off-by`, it automatically appends it (using `git config user.name` / `user.email`). Forgetting `-s` won't cause CI rejection. Install:

```sh
git config core.hooksPath .githooks
```

After installation, a regular `git commit` works—the hook automatically adds the sign-off at the end:

```console
$ git commit -m "feat: your feature description"
commit-msg: appended Signed-off-by: Your Name <your.email@example.com>
```

If the hook prompts `git user.name and user.email must be set`, configure your identity first:

```sh
git config user.name "Your Name"
git config user.email "you@example.com"
```

> `git commit --no-verify` can bypass the hook, but CI will still reject unsigned commits—do not rely on it.

## Pull Request Process

### Branch and Promotion Strategy

Daily development follows this branch flow:

```text
feat/*, fix/* ──PR──> dev ──promotion PR──> main
hotfix/* (from main) ──PR──> main
main ──sync PR──> dev
```

Regular features and fixes are merged from `feat/*` or `fix/*` branches via PR into `dev`, then promoted to `main` via a promotion PR after integration verification in dev. Emergency fixes are created from `main` as `hotfix/*`, merged via PR into `main`, and then a sync PR must merge `main` back to `dev`. Regular PRs use squash merge; `dev → main` promotions and `main → dev` syncs use merge commits to preserve release history and avoid re-displaying already-released commits. Do not push directly to `main` or `dev`.

1. **Create a branch**
   ```sh
   git checkout -b feat/your-feature
   ```

2. **Write code and test**
   ```sh
   go test -race ./...
   go build ./...
   ```

3. **Commit changes** (remember `-s`)
   ```sh
   git add .
   git commit -s -m "feat: your feature description"
   ```

4. **Push and create PR**
   ```sh
   git push origin feat/your-feature
   ```
   Then create a Pull Request on GitHub (target is usually `dev`; `hotfix/*` targets `main`), following the [PR template](PULL_REQUEST_TEMPLATE.md).

5. **Wait for review**
    - CI checks must pass (test / race / vet / cross-build / DCO)
    - Follow the review, status checks, and bypass rules configured in the repository at the time; this document does not set a fixed number of reviewers or bypass rules

## Directory Structure

```
.
├── cmd/mihari/           # Main program entry: dependency assembly, startup, process exit
├── internal/
│   ├── app/              # Use case orchestration independent of presentation layer
│   ├── buildinfo/        # Build-time injected version information
│   ├── cli/              # cobra command definitions
│   ├── config/           # Settings loading, validation, and atomic persistence
│   ├── control/          # Local control protocol (protocol/server/client/credential/transport)
│   ├── core/             # mihomo core installation/update
│   ├── daemon/           # Component lifecycle, startup order, graceful shutdown
│   ├── elevate/          # Privilege elevation detection/prompts
│   ├── geoip/            # Local GeoIP data download and validation
│   ├── integration/      # Cross-domain integration use cases
│   ├── mihomo/           # mihomo REST/WebSocket adapter
│   ├── onboarding/       # First-time setup flow
│   ├── panel/            # Web panel installation and version management
│   ├── platform/         # Platform paths, browser opening, etc.
│   ├── preferences/      # User preferences
│   ├── runtime/          # Runtime mutation orchestration and cross-domain transactions
│   ├── service/          # System service (kardianos/service wrapper)
│   ├── state/            # State management
│   ├── subscription/     # Subscription model, cache, generation, refresh, switch
│   ├── supervisor/       # Subprocess, health check, restart and backoff
│   ├── sysproxy/         # System proxy (Windows registry / GNOME / networksetup)
│   ├── tui/              # Terminal interactive interface (bubbletea)
│   ├── update/           # mihari self-update (GitHub Releases)
│   └── web/              # Web gateway and panel static hosting
├── scripts/install/      # One-click install scripts (install / install-aio / install-aio-remote, .sh + .ps1)
└── go.mod                # Dependency declaration
```

## Release Process (Maintainers Only)

See [docs/RELEASE.md](../docs/RELEASE.md).

## Issue Reporting

- Bug reports: Use [GitHub Issues](https://github.com/mihari-proxy/mihari/issues)
- Feature requests: Also use Issues
- Security vulnerabilities: See [SECURITY.md](SECURITY.md)

## License

This project uses [GPL-3.0](../LICENSE). By submitting a PR to contribute code, you agree to publish your contribution under the same license (inbound = outbound).