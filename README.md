# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. Users should not need to care whether a tool was written in Node.js, Rust, Go, Python, or something else.

The CLI is `prl`.

```powershell
prl install starterqueue
prl install ripgrep
prl update ripgrep
prl remove ripgrep
prl search queue
prl info ripgrep
prl package inspect VyxorDev/starterqueue
prl package generate VyxorDev/starterqueue
prl package add VyxorDev/starterqueue
prl runtime install python ">=3.11"
prl runtime list
prl registry status
prl registry sync
prl list
prl doctor
```

## Status

Packrail is currently **v0.6-alpha**.

The user-side lifecycle is:

`GitHub registry -> local cache -> platform resolve -> runtime resolve -> download -> verify -> safe extract -> shim -> run -> update/remove`

The publisher-side flow is:

`GitHub repository -> latest Release -> detect artifacts -> inspect metadata -> generate -> validate -> add/update registry metadata`

The registry currently proves two different distribution models:

- **StarterQueue** — a Node.js CLI distributed from GitHub Releases. Packrail automatically acquires and isolates a compatible Node runtime.
- **ripgrep** — a native executable. Packrail selects the correct upstream artifact for Windows, Linux, or macOS and for x64 or ARM64.

## Install, update, and remove

Install a package with one command:

```powershell
prl install ripgrep
```

Update one package or every installed package:

```powershell
prl update ripgrep
prl update
```

Remove a package:

```powershell
prl remove ripgrep
```

Packrail records each installation in `.packrail.json`. Removal uses those records instead of trying to guess which files belong to a package. Package directories and command shims are removed deterministically. Shared managed runtime caches are intentionally retained because another installed package may use them.

Updates resolve the current registry version, install it through the normal verified path, then remove stale package versions. A package already at the registry version is left untouched.

## GitHub Release inspection and package authoring

Packrail can inspect a public GitHub repository's latest Release and classify supported `.zip`, `.tgz`, and `.tar.gz` assets by platform and architecture.

```powershell
prl package inspect BurntSushi/ripgrep
```

It recognizes common release naming conventions for:

```text
windows-amd64
windows-arm64
linux-amd64
linux-arm64
darwin-amd64
darwin-arm64
any
```

GitHub-published SHA-256 asset digests are carried into generated package metadata.

Generate a registry draft:

```powershell
prl package generate VyxorDev/starterqueue
```

Or generate, validate, and upsert it directly into a registry JSON file:

```powershell
prl package add VyxorDev/starterqueue
```

By default `package add` updates:

```text
internal/registry/data/packages.json
```

A different registry file can be used with:

```powershell
prl package add owner/tool --registry path/to/packages.json
```

For npm-style `.tgz` release artifacts, Packrail reads `package/package.json` directly from the archive and can infer:

- package name and version
- description
- CLI command
- CLI entrypoint
- Node.js version requirement
- release artifact URL
- SHA-256 digest

That means a project can publish an npm-pack-compatible tarball to **GitHub Releases without publishing to npm**, and Packrail can reconstruct most or all of the install metadata automatically.

For native tools whose repository name differs from their executable, the command can be supplied explicitly:

```powershell
prl package add BurntSushi/ripgrep --command rg
```

`package add` refuses incomplete generated definitions and validates the complete resulting registry before replacing the file. The registry file remains a normal Git-tracked artifact, so its diff can be reviewed and committed like source code.

## Live GitHub registry

Package metadata is not locked inside a specific `prl` build. Packrail treats the registry stored in this GitHub repository as the authoritative source and caches the last valid copy locally.

```powershell
prl registry sync
prl registry status
```

The cache lives under:

```text
~/.packrail/registry/packages.json
```

Normal `install`, `search`, `info`, and `update` commands can use the live/cached registry. If GitHub is temporarily unavailable, Packrail falls back to the last valid cache. A small embedded registry snapshot remains as a first-run/offline bootstrap.

Remote registry downloads are validated before they can replace the cache. Packrail rejects duplicate package names, insecure artifact URLs, invalid SHA-256 values, unsupported archive formats, missing entrypoints, and incomplete package definitions.

Adding or updating a package in the GitHub registry does **not** require publishing a new Packrail binary.

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

Packrail's runtime manager supports four runtime families:

```text
Node.js
Python
Java
.NET
```

They can be acquired explicitly:

```powershell
prl runtime install node ">=20"
prl runtime install python ">=3.11"
prl runtime install java ">=21"
prl runtime install dotnet ">=8"
prl runtime list
```

Managed runtimes are stored privately under:

```text
~/.packrail/runtimes/<runtime>/
```

Packrail uses upstream runtime distributions and verifies their published checksums before installation:

- Node.js — official Node.js distributions and `SHASUMS256.txt`
- Python — portable Python Build Standalone release archives
- Java — Eclipse Temurin JRE archives from the Adoptium API
- .NET — Microsoft runtime archives resolved from official release metadata

Node-based packages already use this automatically. For example, this works even when Node is not globally installed:

```powershell
prl install starterqueue
```

The generic runtime resolver is also available to package manifests for Python, Java, and .NET. Automatic publisher inference for those ecosystems is not yet as rich as the current npm/Node inference, and some package types may need additional launch metadata in future manifest revisions.

## Packrail binaries

Packrail's release workflow builds native `prl` binaries for:

```text
prl-windows-amd64.exe
prl-windows-arm64.exe
prl-linux-amd64
prl-linux-arm64
prl-macos-amd64
prl-macos-arm64
```

Each binary release also includes:

```text
SHA256SUMS.txt
```

The release workflow runs the Go test suite and vet before producing binaries, then cross-compiles with `CGO_ENABLED=0`.

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
- Publishing should begin from metadata projects already have, not from ecosystem-specific bureaucracy.

## Build

Packrail targets Go 1.23+.

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

CI tests Windows, Linux, and macOS. It verifies registry synchronization, GitHub Release package generation/authoring, lifecycle removal/update behavior, and end-to-end installs for both a managed-runtime package (StarterQueue) and a native platform package (ripgrep).

## Current limitations

Packrail remains alpha software. Package artifacts currently support `.tgz` and `.zip`. Runtime constraints currently resolve minimum compatible versions rather than implementing a complete ecosystem-specific constraint grammar. GitHub Release package generation currently focuses on public Releases and has the richest automatic inference for npm-style Node CLIs and conventional native binaries. Java packages that require special launch flags such as `-jar`, Python packaging conventions beyond executable scripts, direct `github:owner/repo` installs, runtime garbage collection, transactional rollback across every filesystem failure, and richer package formats still need additional work.

## License

MIT
