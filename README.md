# Packrail

**Universal package distribution for open-source software.**

Packrail is an experimental, Git-native distribution layer for open-source command-line software. The project is designed around a simple idea: users should not need to care whether a tool was published to npm, PyPI, crates.io, Homebrew, or another ecosystem.

The CLI is `prl`.

```powershell
prl install starterqueue
prl search queue
prl info starterqueue
prl list
prl doctor
```

## Status

Packrail is currently **v0.2-alpha**.

The install path now looks like:

`registry -> resolve -> runtime -> download -> SHA-256 verify -> extract -> shim -> run`

The initial registry contains StarterQueue, distributed directly from its GitHub Release rather than npm.

### Managed runtimes

Packrail can now install its own private Node.js runtime when a package requires Node. The runtime is downloaded from the official Node.js distribution service, verified against the release's published SHA-256 checksum, and stored under the Packrail home directory.

That means this should work even when Node is not installed globally:

```powershell
prl install starterqueue
```

Packrail keeps the runtime isolated under:

```text
~/.packrail/runtimes/node/
```

Generated command shims point directly to the Packrail-managed runtime instead of relying on `node` from the user's PATH.

## Principles

- GitHub-first and zero required server infrastructure.
- One native `prl` binary.
- Package artifacts stay with their upstream projects whenever possible.
- Verification is mandatory, not optional.
- Installations and managed runtimes live under the user's Packrail home instead of spraying files across the machine.
- No arbitrary post-install scripts in the package format.
- Language runtimes should be invisible implementation details for users whenever Packrail can manage them safely.

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

CI tests Windows, Linux, and macOS. It also performs an end-to-end smoke install of StarterQueue using a Packrail-managed Node runtime.

## Current limitations

The alpha package installer currently supports `.tgz` / tar+gzip package artifacts, and managed runtime acquisition currently supports Node.js only. Updates, removals, direct GitHub installs, additional runtime families, richer package formats, and registry PR validation are planned next.

## License

MIT
