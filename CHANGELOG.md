# Changelog

All notable Packrail changes are documented here.

## 1.0.0 - 2026-10-31

### Added
- `prl sync --dry-run` previews the exact lockfile installation plan without downloading, installing, recovering journals, or modifying project files.
- `prl install owner/repo` installs directly from the latest GitHub Release; the explicit `github:owner/repo` syntax remains available.

### Improved
- The release workflow publishes versions with a prerelease suffix as GitHub prereleases and plain semantic versions as stable releases.
- The first stable release includes Windows, Linux, and macOS x64/ARM64 binaries plus version-pinned installers and SHA-256 checksums.
- Project sync planning refuses to hide an interrupted metadata transaction; normal sync must recover it first.
- Manifest-aware project operations use a cross-process operating-system lock to prevent concurrent commands from overwriting each other's manifest/lockfile changes.
