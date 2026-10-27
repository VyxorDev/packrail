# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. The CLI is `prl`.

```powershell
prl install ripgrep
prl install github:BurntSushi/ripgrep
prl install github:VyxorDev/starterqueue
prl package submit owner/tool
prl update
prl remove ripgrep
prl self update
```

## Status

Packrail is in **v0.9.0-alpha development**. The latest published installer release remains **v0.8.0-alpha** until v0.9 release assets are published.

The user-side flow is:

`package name or GitHub repository -> platform resolve -> runtime resolve -> verified download -> safe extract -> launcher -> run -> update/remove`

The publisher-side flow is:

`GitHub repository -> latest Release -> detect artifacts -> infer metadata/runtime/entrypoint -> validate provenance -> create registry manifest/index update -> GitHub PR`

## Install Packrail

Installer scripts are attached to each Packrail release and pinned to that release version. They do not depend on a mutable `main` installer script.

### Windows

```powershell
irm https://github.com/VyxorDev/packrail/releases/download/v0.8.0-alpha/install.ps1 | iex
```

The installer supports built-in Windows PowerShell 5.1 and modern PowerShell. It selects the correct x64/ARM64 release binary, downloads `SHA256SUMS.txt`, verifies the binary, installs `prl.exe` under `%LOCALAPPDATA%\Packrail\bin` by default, and adds that directory to the user PATH.

### Linux / macOS

```bash
curl -fsSL https://github.com/VyxorDev/packrail/releases/download/v0.8.0-alpha/install.sh | sh
```

The Unix installer selects Linux/macOS and x64/ARM64 automatically, verifies the release checksum, installs to `~/.local/bin/prl` by default, and adds that directory to PATH through `~/.profile` when necessary.

Set `PACKRAIL_INSTALL_DIR` before running either script to choose another destination.

## Project lockfiles (v0.9 alpha)

Use `prl add` from a project directory to install a tool and record its complete resolved package metadata in `packrail.lock`:

    prl add ripgrep
    prl add starterqueue
    prl add github:owner/repo
    prl lock list
    prl lock validate
    prl sync

`prl install` remains a one-off install and does not edit the current directory. `prl add` creates or updates the project lockfile after a successful install. `prl lock list` shows locked entries, `prl lock validate` checks the file, and `prl lock remove <package>` removes only a lockfile entry; it deliberately leaves the shared global installation untouched. `prl remove <package>` still removes the global installation. Commit `packrail.lock` to your project so another machine can run `prl sync` from the same directory.

### Lockfile v2 reproducibility

Lockfile version 2 pins the package version, source, command/entrypoint metadata, platform artifact URLs, formats, and package checksums. For packages that use a Packrail-managed runtime, it also records the exact Node.js, Python, Java, or .NET runtime artifact for each supported target platform: exact runtime version, immutable download URL, checksum, archive format, and executable metadata.

`prl sync` loads those saved definitions and installs from the lockfile rather than asking the live registry or runtime provider to choose a newer compatible release. A matching package/runtime installation is skipped; if the runtime pin changes, sync treats the old installation as stale and reconciles it to the newly pinned version.

Version 1 lockfiles remain readable for compatibility. Because v1 did not contain exact runtime artifacts, managed runtimes in a v1 file retain the original constraint-based resolution behavior. Running `prl add` against that project upgrades the file to v2 and resolves the exact runtime pins.

Runtime and package downloads are verified before replacement. Failed checksum verification, unsafe archives, failed extraction, or an unavailable runtime artifact do not intentionally delete a previously working package/runtime installation. Shared managed runtimes remain cached because other projects may still depend on them.

Direct `prl add github:owner/repo` requires enough safe metadata to describe the supported platforms; use `prl install github:owner/repo` for a current-platform-only one-off install when the release cannot be locked portably.

## Registry installs

```powershell
prl install starterqueue
prl install ripgrep
```

Packrail resolves the current machine, verifies the artifact, installs it under `~/.packrail`, and creates the command shim.

The live registry is GitHub-backed and cached locally:

```powershell
prl registry sync
prl registry status
```

The authoritative flattened registry is `registry/index.json`. If GitHub is unavailable, Packrail can fall back to the last valid cache or its embedded bootstrap registry.

## Direct GitHub installs

A suitable public GitHub Release can be installed without first adding the project to Packrail's registry:

```powershell
prl install github:BurntSushi/ripgrep
prl install github:VyxorDev/starterqueue
```

For direct installs Packrail inspects only the current machine's release artifact. It can infer conventional platform names, prefer normal GNU Linux artifacts over musl variants, detect a unique native executable even when the repository name differs from the command (`ripgrep` -> `rg`), and reuse runtime inference for supported script/runtime layouts.

A direct install is refused when Packrail cannot infer the release safely. It does not silently guess through ambiguous archives.

## Managed runtimes and universal launchers

Packrail can privately acquire and checksum-verify:

```text
Node.js
Python
Java
.NET
```

```powershell
prl runtime install node ">=20"
prl runtime install python ">=3.11"
prl runtime install java ">=21"
prl runtime install dotnet ">=8"
prl runtime list
```

Managed runtimes live under:

```text
~/.packrail/runtimes/<runtime>/
```

Package command metadata supports runtime launch arguments and an `{entrypoint}` placeholder, allowing one launcher model to express:

```text
node app.js
python app.py
java -jar app.jar
dotnet app.dll
native-executable
```

Packrail currently infers obvious `.py`, `.jar`, and command-matching `.dll` entrypoints conservatively. Node/npm-style tarballs still have the richest metadata inference because `package.json` directly describes their CLI and Node requirement.

## Update, remove, and self-update

```powershell
prl update ripgrep
prl update
prl remove ripgrep
prl self update
```

Packrail records installations in `.packrail.json`; removal uses those records instead of guessing which files belong to a package. Shared managed runtimes remain cached because another installed package may use them.

Self-update selects the correct Packrail release binary, verifies it against `SHA256SUMS.txt`, and replaces the current executable with rollback protection if replacement fails.

## GitHub Release inspection and local package authoring

Inspect a public release:

```powershell
prl package inspect BurntSushi/ripgrep
```

Generate package metadata:

```powershell
prl package generate BurntSushi/ripgrep --command rg
```

Write or update a canonical package manifest and rebuild the deterministic registry index:

```powershell
prl package add BurntSushi/ripgrep --command rg
prl registry build
prl registry validate --online
```

The canonical authoring layout is:

```text
registry/
├── packages/
│   ├── ripgrep.json
│   └── starterqueue.json
└── index.json
```

`registry/index.json` is generated from the individual package manifests. Registry history rejects package deletion, source reassignment, and same-version metadata rewrites.

## Self-service registry submission

A GitHub repository owner or collaborator with push access can submit a generated package to Packrail's registry directly from `prl`:

```powershell
prl package submit owner/tool
```

Authentication is discovered from `PACKRAIL_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, or an existing `gh auth token` session. Packrail verifies the authenticated GitHub identity can push to the source repository before creating the submission.

The submit command:

1. inspects the latest GitHub Release;
2. generates and validates the Packrail package manifest;
3. verifies that package artifacts belong to the same GitHub source repository;
4. verifies publisher access to that source repository;
5. creates or reuses the submitter's Packrail fork;
6. creates a dedicated registry branch;
7. writes `registry/packages/<name>.json` and the rebuilt `registry/index.json`;
8. opens a pull request against `VyxorDev/packrail:main`.

The permanent Registry Gate CI independently checks registry-only changes, deterministic index generation, online source/artifact reachability, existing package immutability, and PR-author access to the package source repository.

## Runtime acquisition

Runtime downloads are checksum-verified and stored under Packrail's private runtime directory. Current runtime providers are:

- Node.js from official Node distributions;
- Python from Python Build Standalone install-only archives;
- Java from Eclipse Temurin / Adoptium JRE releases;
- .NET from Microsoft's official release metadata and runtime archives.

## Safety model

Packrail deliberately avoids arbitrary post-install package scripts. Downloads are checksum-verified, archive extraction rejects path traversal, ZIP symlinks are rejected, and TGZ symlink/hardlink entries are rejected. Package names, versions, command names, entrypoints, and v2 runtime pins are validated before they can become filesystem paths or download targets.

GitHub-backed package manifests use HTTPS sources and artifacts. Registry validation binds GitHub release artifacts to the package source repository. Lockfile v2 runtime artifacts also require HTTPS URLs and valid published checksums before installation.

## Environment variables

```text
PACKRAIL_HOME
PACKRAIL_REGISTRY_URL
PACKRAIL_REGISTRY_OFFLINE
PACKRAIL_INSTALL_DIR
PACKRAIL_INSTALL_VERSION
PACKRAIL_GITHUB_TOKEN
GH_TOKEN
GITHUB_TOKEN
```

`PACKRAIL_HOME` changes Packrail's managed state root. `PACKRAIL_INSTALL_DIR` changes the bootstrap install destination. `PACKRAIL_INSTALL_VERSION` pins the bootstrap script to an explicit release and is injected automatically into release-attached installers.

## Development

Packrail is written in Go. The project currently depends only on the Go standard library.

```powershell
go test ./...
go vet ./...
go build ./cmd/prl
```

CI runs tests, vet/build, publisher/registry smoke tests, managed runtime installation, lifecycle checks, native package installation, native lockfile sync, and managed-runtime lockfile sync across Windows, Linux, and macOS.

Packrail is still alpha software. Manifest inference is intentionally conservative, runtime version constraints are not yet a complete semver solver, runtime garbage collection is not implemented yet, and direct GitHub installation refuses ambiguous releases instead of guessing.
