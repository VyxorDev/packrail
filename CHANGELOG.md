# Changelog

All notable Packrail changes are documented here.

## Unreleased

### Added
- `prl sync --dry-run` previews the exact lockfile installation plan without downloading, installing, recovering journals, or modifying project files.
- `prl install owner/repo` installs directly from the latest GitHub Release; the explicit `github:owner/repo` syntax remains available.

### Improved
- The release workflow publishes versions with a prerelease suffix as GitHub prereleases and plain semantic versions as stable releases.
- Project sync planning refuses to hide an interrupted metadata transaction; normal sync must recover it first.
- Manifest-aware project operations use a cross-process operating-system lock to prevent concurrent commands from overwriting each other's manifest/lockfile changes.
