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

Packrail is currently **v0.1-alpha**. The first milestone proves the end-to-end path:

`registry -> resolve -> download -> SHA-256 verify -> extract -> shim -> run`

The initial registry contains StarterQueue, distributed directly from its GitHub Release rather than npm.

## Principles

- GitHub-first and zero required server infrastructure.
- One native `prl` binary.
- Package artifacts stay with their upstream projects whenever possible.
- Verification is mandatory, not optional.
- Installations live under the user's Packrail home instead of spraying files across the machine.
- No arbitrary post-install scripts in the package format.

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

## Current limitations

The alpha installer currently supports `.tgz` / tar+gzip packages and expects required language runtimes (for example Node.js) to already exist. Runtime acquisition, updates, removals, direct GitHub installs, richer package formats, and registry PR validation are planned next.

## License

MIT
