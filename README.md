# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. Users should not need to care whether a tool was written in Node.js, Rust, Go, Python, or something else.

The CLI is `prl`.

```powershell
prl install starterqueue
prl install ripgrep
prl search queue
prl info ripgrep
prl list
prl doctor
```

## Status

Packrail is currently **v0.3-alpha**.

The install path is:

`registry -> platform resolve -> runtime resolve -> download -> SHA-256 verify -> safe extract -> shim -> run`

The registry currently proves two different distribution models:

- **StarterQueue** — a Node.js CLI distributed from GitHub Releases. Packrail automatically acquires and isolates a compatible Node runtime.
- **ripgrep** — a native executable. Packrail selects the correct upstream artifact for Windows, Linux, or macOS and for x64 or ARM64.

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

CI tests Windows, Linux, and macOS. It performs end-to-end installs for both a managed-runtime package (StarterQueue) and a native platform package (ripgrep).

## Current limitations

The alpha installer currently supports `.tgz` and `.zip` artifacts. Managed runtime acquisition currently supports Node.js only. Updates, removals, direct GitHub installs, additional runtime families, registry validation tooling, and richer package formats are planned next.

## License

MIT
