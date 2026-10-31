# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. The CLI is `prl`.

```powershell
prl install ripgrep
prl install BurntSushi/ripgrep
prl install github:BurntSushi/ripgrep
prl init
prl add ripgrep@15.2.0
prl sync
prl update
prl package submit owner/tool
prl self update
```

## Status

The Packrail CLI currently reports **v0.9.0-alpha**. The project manifest/lockfile architecture is implemented on `main`; the latest published installer release remains **v0.8.0-alpha**. The current work is release hardening, not another feature expansion.

The user-side flow is:

`dependency intent -> package/runtime resolution -> verified immutable lock -> safe install -> launcher -> sync/update/remove`

The publisher-side flow is:

`GitHub repository -> Release -> detect artifacts -> infer metadata/runtime/entrypoint -> validate provenance -> create registry manifest/index update -> GitHub PR`

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

## Project manifests and lockfiles

Packrail now separates **what a project asks for** from **what Packrail resolved**:

```text
packrail.json  -> human-editable dependency intent
packrail.lock  -> machine-generated exact resolved state
```

Start a project and add tools:

```powershell
prl init
prl add ripgrep
prl add ripgrep@15.2.0
prl add starterqueue
prl add github:owner/repo
prl sync
```

A generated `packrail.json` uses schema version 1. For example:

```json
{
  "manifest_version": 1,
  "dependencies": {
    "ripgrep": {
      "constraint": "^15"
    },
    "starterqueue": {
      "constraint": "*",
      "repo": "VyxorDev/starterqueue"
    }
  }
}
```

The loader also accepts a concise string form such as `"ripgrep": "^15"`; Packrail currently writes the explicit object form so direct source identity can use the same schema.

`prl install` remains a one-off/global install and never edits project files. In a normal project, `prl add` resolves the requested dependency, validates the prospective lock state, installs it safely, and only then commits the project metadata. Fresh directories can use `prl add` directly; `prl init` is useful when creating an empty manifest or explicitly migrating a v0.9 lockfile-only project.

### Exact sync vs explicit update

`prl sync` is deliberately boring: it validates `packrail.json` against `packrail.lock` and installs **exactly** what the lockfile says. It does not ask the registry or a runtime provider for a newer version, and it does not rewrite the lock merely because a newer release exists.

```powershell
prl sync
```

Preview the same operation without downloading or changing anything:

```powershell
prl sync --dry-run
```

The dry run validates manifest/lock agreement and inspects whether each exact locked package is already installed. It reports what would be installed or updated, but does not recover transaction journals, download artifacts, install packages, or modify project files. If an interrupted transaction needs recovery, run normal `prl sync` first.

Re-resolution is explicit:

```powershell
prl update
prl update ripgrep
prl update ripgrep starterqueue
```

Inside a project, `prl update` resolves the existing manifest constraints again and updates `packrail.lock`; the dependency intent in `packrail.json` remains unchanged. Outside a manifest project, the existing global `prl update [package]` behavior remains available.

If the manifest and lockfile disagree, `prl sync` refuses to guess or silently repair them. Use `prl update` to deliberately resolve current intent into a new lockfile.

### Removing project dependencies

```powershell
prl remove ripgrep
prl remove ripgrep --uninstall
```

Inside a project, normal `prl remove` removes the dependency from `packrail.json` and its resolved lock entry but deliberately leaves the shared/global Packrail installation intact. `--uninstall` explicitly removes that shared installation too. Outside a manifest project, `prl remove <package>` retains the historical global-uninstall behavior.

`prl lock list`, `prl lock validate`, and `prl lock remove` remain low-level lockfile tools. For normal manifest projects prefer `prl remove`; manually editing/removing only a lock entry can intentionally make the manifest and lock disagree, which `prl sync` will detect and reject.

### Version constraints and registry history

The project manifest currently understands a deliberately small dependency grammar:

```text
*
15.2.0
^15.2.0
>=15.2.0
```

This syntax does **not** pretend Packrail already has npm-style historical registry storage. The current Packrail registry publishes one resolved version per package. A registry request succeeds only if that published version satisfies the requested constraint; Packrail returns an explicit error instead of silently substituting another version when it does not.

Direct GitHub project dependencies have a stronger exact-version path. For global one-off installs, `prl install owner/repo` selects the latest compatible GitHub Release; the explicit `github:owner/repo` form remains supported. An exact project request such as:

```powershell
prl add github:owner/repo@1.2.3
```

can inspect the matching GitHub Release tag and lock that historical release when its metadata and artifacts can be inferred safely. Non-exact direct GitHub constraints are checked against the current resolved release.

### v0.9 migration and transaction recovery

Existing **lockfile-only** projects remain valid: `prl sync` continues to sync them without requiring a manifest. Running `prl init` beside an existing v0.9 `packrail.lock` creates `packrail.json` using the exact locked package versions as intent. It does not invent wildcard/caret ranges or claim direct-source intent that the old lockfile cannot prove.

Operations that change both project files use a small recovery journal (`.packrail.transaction`). The old manifest/lock bytes are recorded before commit. If the second file cannot be committed, Packrail restores the old pair; if a process is interrupted with a prepared journal, the next project operation restores the previously valid state before doing new work. Package installation itself occurs before the metadata commit and uses Packrail's existing checksum/extraction recovery machinery.

### Lockfile v2 reproducibility

Lockfile version 2 pins the package version, source, command/entrypoint metadata, platform artifact URLs, formats, and package checksums. For packages that use a Packrail-managed runtime, it also records the exact Node.js, Python, Java, or .NET runtime artifact for each supported target platform: exact runtime version, immutable download URL, checksum, archive format, and executable metadata.

A matching package/runtime installation is skipped. If the exact runtime pin identity changes, including its URL/checksum, sync treats the old installation as stale rather than accepting version-only equality.

Version 1 lockfiles remain readable for compatibility. Because v1 did not contain exact runtime artifacts, managed runtimes in a v1 file retain the historical constraint-based runtime resolution behavior until the project is deliberately rewritten into newer locked state.

Runtime and package downloads are verified before replacement. Failed checksum verification, unsafe archives, failed extraction, or an unavailable runtime artifact do not intentionally delete a previously working package/runtime installation. Shared managed runtimes remain cached because other projects may still depend on them.

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
prl install BurntSushi/ripgrep
prl install VyxorDev/starterqueue

# Explicit equivalent syntax remains supported:
prl install github:BurntSushi/ripgrep
```

For one-off direct installs Packrail inspects the current machine's release artifact. It can infer conventional platform names, prefer normal GNU Linux artifacts over musl variants, detect a unique native executable even when the repository name differs from the command (`ripgrep` -> `rg`), and reuse runtime inference for supported script/runtime layouts.

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

## Global update, remove, and self-update

Outside a project manifest, the existing global package commands remain available:

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

Packrail deliberately avoids arbitrary post-install package scripts. Downloads are checksum-verified, archive extraction rejects path traversal, ZIP symlinks are rejected, and TGZ symlink/hardlink entries are rejected. Package names, versions, command names, entrypoints, manifest dependency identifiers, and v2 runtime pins are validated before they can become filesystem paths or download targets.

`packrail.json` and `packrail.lock` are treated as untrusted project input. Unknown manifest fields are rejected, direct GitHub identifiers are validated, manifest/lock agreement is checked before project sync, and exact GitHub artifact provenance remains bound to the package source repository. Lockfile v2 runtime artifacts require HTTPS URLs and published checksums before installation.

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

CI runs tests, vet/build, publisher/registry smoke tests, managed runtime installation, global lifecycle checks, native package installation, project manifest lifecycle tests, and managed-runtime lockfile sync across Windows, Linux, and macOS.

Packrail is still alpha software. Manifest/release inference is intentionally conservative, dependency constraints intentionally cover only a small useful subset rather than a complete semver solver, the registry does not yet retain historical versions, runtime/package garbage collection is not implemented yet, and direct GitHub installation refuses ambiguous releases instead of guessing.
