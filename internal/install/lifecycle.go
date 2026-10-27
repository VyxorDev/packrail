package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
)

// InstalledRecords returns every valid Packrail install record found beneath
// PACKRAIL_HOME/packages. Corrupt or incomplete records are skipped rather than
// guessed at.
func InstalledRecords() ([]InstallRecord, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	packagesDir := filepath.Join(home, "packages")
	packageEntries, err := os.ReadDir(packagesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read installed packages: %w", err)
	}

	var records []InstallRecord
	for _, packageEntry := range packageEntries {
		if !packageEntry.IsDir() {
			continue
		}
		versionsDir := filepath.Join(packagesDir, packageEntry.Name())
		versions, err := os.ReadDir(versionsDir)
		if err != nil {
			continue
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			recordPath := filepath.Join(versionsDir, versionEntry.Name(), ".packrail.json")
			data, err := os.ReadFile(recordPath)
			if err != nil {
				continue
			}
			var record InstallRecord
			if err := json.Unmarshal(data, &record); err != nil || strings.TrimSpace(record.Name) == "" || strings.TrimSpace(record.Version) == "" {
				continue
			}
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Name == records[j].Name {
			return records[i].Version < records[j].Version
		}
		return records[i].Name < records[j].Name
	})
	return records, nil
}

func RecordsFor(name string) ([]InstallRecord, error) {
	records, err := InstalledRecords()
	if err != nil {
		return nil, err
	}
	var matches []InstallRecord
	for _, record := range records {
		if strings.EqualFold(record.Name, name) {
			matches = append(matches, record)
		}
	}
	return matches, nil
}

// IsCurrent reports whether the installed package matches the exact locked
// package metadata and platform artifact, whether its command shim still exists,
// and, for lockfile v2, whether its managed runtime matches the exact runtime
// version pinned for this platform. Older records without a metadata fingerprint
// are reinstalled once.
func IsCurrent(pkg registry.Package) (bool, error) {
	artifact, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return false, err
	}
	entrypoint, err := registry.EntrypointFor(pkg, artifact)
	if err != nil {
		return false, err
	}
	fingerprint, err := packageMetadataSHA256(pkg)
	if err != nil {
		return false, fmt.Errorf("fingerprint package metadata: %w", err)
	}
	lockedRuntimeVersion, runtimePinned, err := packruntimes.LockedVersion(pkg.Runtime)
	if err != nil {
		return false, err
	}
	records, err := RecordsFor(pkg.Name)
	if err != nil {
		return false, err
	}
	home, err := Home()
	if err != nil {
		return false, err
	}
	entryInfo, err := os.Stat(filepath.Join(home, "packages", pkg.Name, pkg.Version, filepath.FromSlash(entrypoint)))
	if err != nil || entryInfo.IsDir() {
		return false, nil
	}
	shimName := pkg.Command.Name
	if runtime.GOOS == "windows" {
		shimName += ".cmd"
	}
	shimInfo, err := os.Stat(filepath.Join(home, "bin", shimName))
	if err != nil || shimInfo.IsDir() {
		return false, nil
	}

	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	for _, record := range records {
		if !strings.EqualFold(record.Name, pkg.Name) ||
			record.Version != pkg.Version ||
			record.Platform != platform ||
			!strings.EqualFold(record.ArtifactSHA256, artifact.SHA256) ||
			record.MetadataSHA256 != fingerprint ||
			!strings.EqualFold(record.Command, pkg.Command.Name) {
			continue
		}
		if runtimePinned && (!record.ManagedRuntime || record.RuntimeVersion != lockedRuntimeVersion) {
			continue
		}
		if record.ManagedRuntime {
			if strings.TrimSpace(record.RuntimeVersion) == "" {
				continue
			}
			versions, err := packruntimes.InstalledVersions(home, record.Runtime)
			if err != nil {
				return false, err
			}
			found := false
			for _, version := range versions {
				if version == record.RuntimeVersion {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// Remove deletes only files tracked beneath a Packrail package directory and
// the command shims recorded for that package. Shared managed runtimes remain
// cached because another installed package may use them.
func Remove(name string) ([]InstallRecord, error) {
	records, err := RecordsFor(name)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("package %q is not installed", name)
	}
	home, err := Home()
	if err != nil {
		return nil, err
	}

	packageRoot := filepath.Join(home, "packages", records[0].Name)
	if err := os.RemoveAll(packageRoot); err != nil {
		return nil, fmt.Errorf("remove package files: %w", err)
	}

	remaining, err := InstalledRecords()
	if err != nil {
		return nil, err
	}
	commandsInUse := map[string]bool{}
	for _, record := range remaining {
		commandsInUse[strings.ToLower(record.Command)] = true
	}
	binDir := filepath.Join(home, "bin")
	seen := map[string]bool{}
	for _, record := range records {
		command := strings.TrimSpace(record.Command)
		if command == "" || seen[strings.ToLower(command)] || commandsInUse[strings.ToLower(command)] {
			continue
		}
		seen[strings.ToLower(command)] = true
		_ = os.Remove(filepath.Join(binDir, command))
		_ = os.Remove(filepath.Join(binDir, command+".cmd"))
	}
	return records, nil
}

// Update installs the registry package if its version is not already the only
// installed version, then removes stale versions while keeping the newly
// generated command shim and shared runtime cache.
func Update(pkg registry.Package) (from []string, changed bool, err error) {
	records, err := RecordsFor(pkg.Name)
	if err != nil {
		return nil, false, err
	}
	if len(records) == 0 {
		return nil, false, fmt.Errorf("package %q is not installed", pkg.Name)
	}

	currentOnly := len(records) == 1 && records[0].Version == pkg.Version
	if currentOnly {
		return []string{pkg.Version}, false, nil
	}
	for _, record := range records {
		from = append(from, record.Version)
	}
	if err := Install(pkg); err != nil {
		return from, false, err
	}

	home, err := Home()
	if err != nil {
		return from, false, err
	}
	packageRoot := filepath.Join(home, "packages", pkg.Name)
	entries, err := os.ReadDir(packageRoot)
	if err != nil {
		return from, false, fmt.Errorf("read package versions after update: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != pkg.Version {
			if err := os.RemoveAll(filepath.Join(packageRoot, entry.Name())); err != nil {
				return from, false, fmt.Errorf("remove old version %s: %w", entry.Name(), err)
			}
		}
	}
	return from, true, nil
}
