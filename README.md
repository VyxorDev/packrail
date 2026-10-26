# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. The CLI is `prl`.

```powershell
prl install ripgrep
prl install github:BurntSushi/ripgrep
prl install github:VyxorDev/starterqueue
prl update
prl remove ripgrep
prl self update
```

## Status

Packrail is currently **v0.7-alpha**.

The user-side flow is:

`package name or GitHub repository -> platform resolve -> runtime resolve -> verified download -> safe extract -> launcher -> run -> update/remove`

The publisher-side flow is:

`GitHub repository -> latest Release -> detect artifacts -> infer metadata/runtime/entrypoint -> validate -> registry metadata`

## Install Packrail

### Windows

```powershell
irm https://raw.githubusercontent.com/VyxorDev/packrail/main/scripts/install.ps1 | iex
```

The installer selects the correct x64/ARM64 release binary, downloads `SHA256SUMS.txt`, verifies the binary, installs `prl.exe` under `%LOCALAPPDATA%\Packrail\bin` by default, and adds that directory to the user PATH.

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/VyxorDev/packrail/main/scripts/install.sh | sh
```

The Unix installer selects Linux/macOS and x64/ARM64 automatically, verifies the release checksum, installs to `~/.local/bin/prl` by default, and adds that directory to PATH through `~/.profile` when necessary.

Set `PACKRAIL_INSTALL_DIR` before running either script to choose another destination.

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

If GitHub is unavailable, Packrail can fall back to the last valid cache or its embedded bootstrap registry.

## Direct GitHub installs

A suitable public GitHub Release can be installed without first adding the project to Packrail's registry:

```powershell
prl install github:BurntSushi/ripgrep
```

or:

```powershell
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

Update one package or every installed registry package:

```powershell
prl update ripgrep
prl update
```

Remove a package:

```powershell
prl remove ripgrep
```

Packrail records installations in `.packrail.json`; removal uses those records instead of guessing which files belong to a package. Shared managed runtimes remain cached because another installed package may use them.

Update Packrail itself:

```powershell
prl self update
```

Self-update reads the repository `VERSION`, selects the correct release binary for the current OS/architecture, verifies it against that release's `SHA256SUMS.txt`, and replaces the running Packrail binary with rollback protection if replacement fails.

## GitHub Release inspection and package authoring

Inspect a public release:

```powershell
prl package inspect BurntSushi/ripgrep
```

Generate package metadata:

```powershell
prl package generate VyxorDev/starterqueue
```

Generate, validate, and upsert registry metadata:

```powershell
prl package add VyxorDev/starterqueue
```

Use a different registry file with:

```powershell
prl package add owner/tool --registry path/to/packages.json
```

For npm-style `.tgz` releases Packrail can read `package/package.json` and infer the package name/version, description, CLI command, CLI entrypoint, Node requirement, artifact URL, and SHA-256 digest. Conventional native and runtime-based archives receive additional conservative inference.

## Platform-aware packages

Packrail understands:

```text
windows-amd64
windows-arm64
linux-amd64
linux-arm64
darwin-amd64
darwin-arm64
any
```

Release binaries for Packrail itself use the same matrix:

```text
prl-windows-amd64.exe
prl-windows-arm64.exe
prl-linux-amd64
prl-linux-arm64
prl-macos-amd64
prl-macos-arm64
SHA256SUMS.txt
```

## Security model

- HTTPS sources are required by the registry.
- Package artifacts require SHA-256 metadata.
- Runtime downloads are verified against upstream checksums/hashes.
- Packrail release installers and self-update verify `SHA256SUMS.txt`.
- Archive extraction blocks path traversal and unsafe links where the package format does not permit them.
- Package definitions do not execute arbitrary post-install scripts.
- Direct GitHub installs fail closed when release inference is ambiguous.

## Principles

- GitHub-first and zero required server infrastructure.
- One native `prl` binary.
- Upstream projects keep their own release artifacts.
- Platform and runtime details should be implementation details, not user chores.
- Verification is mandatory.
- Publishing should begin from metadata projects already have instead of requiring another ecosystem registry.

## Build and development

Packrail targets Go 1.23+.

```bash
go build -o prl ./cmd/prl
go test ./...
go vet ./...
```

CI exercises Windows, Linux, and macOS; registry synchronization; publisher generation; package lifecycle; managed Node installs; native installs; and direct GitHub installs.

## Current limitations

Packrail remains alpha software. Package artifacts currently support `.tgz`/`.tar.gz` and `.zip`. Runtime constraints use a deliberately small compatibility grammar rather than every ecosystem's complete version syntax. Direct GitHub installation works only when the release layout can be inferred safely. Python/Java/.NET publisher inference is intentionally conservative, runtime garbage collection is not implemented yet, and full transactional rollback/lockfiles are future work.

## License

MIT
