# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. Users should not need to care whether a tool was written in Node.js, Rust, Go, Python, or something else.

The CLI is `prl`.

```powershell
prl install starterqueue
prl install ripgrep
prl search queue
prl info ripgrep
prl registry status
prl registry sync
prl list
prl doctor
```

## Status

Packrail is currently **v0.4-alpha**.

The install path is:

`GitHub registry -> local cache -> platform resolve -> runtime resolve -> download -> SHA-256 verify -> safe extract -> shim -> run`

The registry currently proves two different distribution models:

- **StarterQueue** — a Node.js CLI distributed from GitHub Releases. Packrail automatically acquires and isolates a compatible Node runtime.
- **ripgrep** — a native executable. Packrail selects the correct upstream artifact for Windows, Linux, or macOS and for x64 or ARM64.

## Live GitHub registry

Package metadata is no longer locked inside a specific `prl` build. Packrail treats the registry stored in this GitHub repository as the authoritative source and caches the last valid copy locally.

```powershell
prl registry sync
prl registry status
```

The cache lives under:

```text
~/.packrail/registry/packages.json
```

Normal `install`, `search`, and `info` commands automatically refresh stale metadata. If GitHub is temporarily unavailable, Packrail falls back to the last valid cache. A small embedded registry snapshot remains only as a first-run/offline bootstrap.

Remote registry downloads are validated before they can replace the cache. Packrail rejects duplicate package names, insecure artifact URLs, invalid SHA-256 values, unsupported archive formats, missing entrypoints, and incomplete package definitions.

This means adding or updating a package in the GitHub registry does **not** require publishing a new Packrail binary.

## Platform-aware packages

A package can publish different verified artifacts for each target:

```text
windows-amd64
windows-arm64
linux-amd64
linux-arm64
darwin-amd64
darwin-arm64
```

Portable packages can use an `any` artifact instead. Packrail resolves the current machine automatically.

For example:

```powershell
prl install ripgrep
rg --version
```

The user does not need Rust or Cargo. Packrail installs the appropriate prebuilt native release and creates the `rg` command shim.

## Managed runtimes

When a package requires Node.js, Packrail can install a private runtime from the official Node.js distribution service, verify it against the release's published SHA-256 checksum, and store it under:

```text
~/.packrail/runtimes/node/
```

That means this works even when Node is not installed globally:

```powershell
prl install starterqueue
```

Generated command shims point directly to the Packrail-managed runtime instead of relying on `node` from the user's PATH.

## Principles

- GitHub-first and zero required server infrastructure.
- One native `prl` binary.
- GitHub is the registry source; local state is only a validated cache.
- Package artifacts stay with their upstream projects whenever possible.
- Verification is mandatory, not optional.
- Installations and managed runtimes live under the user's Packrail home instead of spraying files across the machine.
- No arbitrary post-install scripts in the package format.
- Language runtimes should be invisible implementation details whenever Packrail can manage them safely.
- Platform differences should be solved by the registry and resolver, not by the user.

## Build

Packrail currently targets Go 1.23+.

```bash
go build -o prl ./cmd/prl
```

On Windows:

```powershell
go build -o prl.exe ./cmd/prl
```

## Development

```bash
go test ./...
go vet ./...
```

CI tests Windows, Linux, and macOS. It verifies registry synchronization and performs end-to-end installs for both a managed-runtime package (StarterQueue) and a native platform package (ripgrep).

## Current limitations

The alpha installer currently supports `.tgz` and `.zip` artifacts. Managed runtime acquisition currently supports Node.js only. Updates, removals, direct GitHub installs, additional runtime families, registry authoring/validation commands, and richer package formats are planned next.

## License

MIT
