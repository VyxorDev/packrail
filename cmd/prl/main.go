package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/install"
	"github.com/VyxorDev/packrail/internal/lockfile"
	"github.com/VyxorDev/packrail/internal/publisher"
	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
	"github.com/VyxorDev/packrail/internal/selfupdate"
	"github.com/VyxorDev/packrail/internal/submit"
)

const version = "0.9.0-alpha"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	switch args[0] {
	case "install", "i":
		if len(args) != 2 {
			return fmt.Errorf("usage: prl install <package|github:owner/repo>")
		}
		target := strings.TrimSpace(args[1])
		var pkg registry.Package
		var err error
		if strings.HasPrefix(strings.ToLower(target), "github:") {
			repo := strings.TrimSpace(target[len("github:"):])
			if repo == "" {
				return fmt.Errorf("usage: prl install github:owner/repo")
			}
			fmt.Printf("Inspecting GitHub release: %s\n", repo)
			draft, draftErr := publisher.New().GenerateForPlatform(repo, "", runtime.GOOS, runtime.GOARCH)
			if draftErr != nil {
				return draftErr
			}
			for _, warning := range draft.Warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
			}
			if !draft.Complete {
				return fmt.Errorf("GitHub release could not be inferred safely; use 'prl package generate %s' to inspect the draft", repo)
			}
			pkg = draft.Package
			fmt.Println("Resolution: direct GitHub Release (registry not required)")
		} else {
			refreshRegistry()
			pkg, err = registry.Find(target)
			if err != nil {
				return err
			}
		}
		if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
			return err
		}
		fmt.Printf("Resolving %s %s\n", pkg.Name, pkg.Version)
		fmt.Printf("Source: %s\n", pkg.Source)
		fmt.Printf("Platform: %s\n", registry.PlatformKey(runtime.GOOS, runtime.GOARCH))
		if pkg.Runtime.Name != "" {
			fmt.Printf("Runtime: %s %s (managed by Packrail)\n", pkg.Runtime.Name, pkg.Runtime.Constraint)
		} else {
			fmt.Println("Runtime: native")
		}
		fmt.Println("Installing...")
		if err := install.Install(pkg); err != nil {
			return err
		}
		home, _ := install.Home()
		fmt.Printf("Installed %s %s\n", pkg.Name, pkg.Version)
		fmt.Printf("Command: %s\n", pkg.Command.Name)
		fmt.Printf("Bin directory: %s\n", filepath.Join(home, "bin"))
		return nil

	case "add":
		return addCommand(args[1:])

	case "sync":
		return syncCommand(args[1:])

	case "update", "upgrade":
		return updateCommand(args[1:])

	case "remove", "uninstall", "rm":
		if len(args) != 2 {
			return fmt.Errorf("usage: prl remove <package>")
		}
		removed, err := install.Remove(args[1])
		if err != nil {
			return err
		}
		versions := make([]string, 0, len(removed))
		for _, record := range removed {
			versions = append(versions, record.Version)
		}
		fmt.Printf("Removed %s (%s)\n", removed[0].Name, strings.Join(versions, ", "))
		fmt.Println("Shared runtime caches were kept for other packages.")
		return nil

	case "info":
		if len(args) != 2 {
			return fmt.Errorf("usage: prl info <package>")
		}
		refreshRegistry()
		pkg, err := registry.Find(args[1])
		if err != nil {
			return err
		}
		artifact, artifactErr := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("%s %s\n", pkg.Name, pkg.Version)
		fmt.Println(pkg.Description)
		fmt.Printf("Source:    %s\n", pkg.Source)
		if pkg.Runtime.Name != "" {
			fmt.Printf("Runtime:   %s %s\n", pkg.Runtime.Name, pkg.Runtime.Constraint)
		} else {
			fmt.Println("Runtime:   native")
		}
		fmt.Printf("Command:   %s\n", pkg.Command.Name)
		fmt.Printf("Platforms: %s\n", strings.Join(registry.Platforms(pkg), ", "))
		if artifactErr == nil {
			fmt.Printf("Artifact:  %s (%s)\n", registry.PlatformKey(runtime.GOOS, runtime.GOARCH), artifact.Format)
			fmt.Printf("SHA-256:   %s\n", artifact.SHA256)
		} else {
			fmt.Printf("Artifact:  unavailable for %s\n", registry.PlatformKey(runtime.GOOS, runtime.GOARCH))
		}
		return nil

	case "search", "s":
		refreshRegistry()
		query := ""
		if len(args) > 1 {
			query = strings.Join(args[1:], " ")
		}
		matches, err := registry.Search(query)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			fmt.Println("No packages found.")
			return nil
		}
		for _, pkg := range matches {
			fmt.Printf("%-20s %-12s %s\n", pkg.Name, pkg.Version, pkg.Description)
		}
		return nil

	case "registry":
		return registryCommand(args[1:])

	case "package":
		return packageCommand(args[1:])

	case "runtime":
		return runtimeCommand(args[1:])

	case "list", "ls":
		return listInstalled()

	case "doctor":
		return doctor()

	case "self":
		if len(args) != 2 || args[1] != "update" {
			return fmt.Errorf("usage: prl self update")
		}
		fmt.Printf("Checking for Packrail updates from %s...\n", version)
		result, err := selfupdate.Update(version)
		if err != nil {
			return err
		}
		if !result.Updated {
			fmt.Printf("Packrail is already up to date (%s).\n", result.Current)
			return nil
		}
		fmt.Printf("Updated Packrail %s -> %s\n", result.Previous, result.Current)
		fmt.Println("Restart prl to use the new binary.")
		return nil

	case "version", "--version", "-v":
		fmt.Printf("prl %s\n", version)
		return nil

	case "help", "--help", "-h":
		printHelp()
		return nil

	default:
		return fmt.Errorf("unknown command %q; run 'prl help'", args[0])
	}
}

func addCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: prl add <package|github:owner/repo>")
	}
	target := strings.TrimSpace(args[0])
	if target == "" {
		return fmt.Errorf("usage: prl add <package|github:owner/repo>")
	}

	pkg, err := resolveForLockfile(target)
	if err != nil {
		return err
	}
	if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}

	path, err := lockfile.Path()
	if err != nil {
		return err
	}
	file, _, err := lockfile.LoadOrEmpty(path)
	if err != nil {
		return err
	}
	if err := lockfile.Upsert(&file, pkg); err != nil {
		return fmt.Errorf("update lockfile: %w", err)
	}

	fmt.Printf("Adding %s %s to the project lockfile...\n", pkg.Name, pkg.Version)
	changed, err := ensureInstalled(pkg)
	if err != nil {
		return fmt.Errorf("install %s %s: %w", pkg.Name, pkg.Version, err)
	}
	if err := lockfile.Save(path, file); err != nil {
		return fmt.Errorf("installed %s %s, but could not save lockfile %s: %w", pkg.Name, pkg.Version, path, err)
	}
	if changed {
		fmt.Printf("Added %s %s to %s\n", pkg.Name, pkg.Version, path)
	} else {
		fmt.Printf("%s %s is already installed; lockfile updated at %s\n", pkg.Name, pkg.Version, path)
	}
	return nil
}

func syncCommand(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: prl sync")
	}
	path, err := lockfile.Path()
	if err != nil {
		return err
	}
	file, err := lockfile.Load(path)
	if err != nil {
		return err
	}
	if len(file.Packages) == 0 {
		return fmt.Errorf("%s contains no packages; add one with 'prl add <package>'", path)
	}

	// Preflight every package before downloading or replacing anything. This
	// avoids a predictable unsupported-platform error halfway through a sync.
	for _, pkg := range file.Packages {
		if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
			return fmt.Errorf("lockfile package %s: %w", pkg.Name, err)
		}
	}

	fmt.Printf("Syncing %d packages from %s...\n", len(file.Packages), path)
	installed, current := 0, 0
	for _, pkg := range file.Packages {
		changed, err := ensureInstalled(pkg)
		if err != nil {
			return fmt.Errorf("sync %s %s: %w", pkg.Name, pkg.Version, err)
		}
		if changed {
			fmt.Printf("Installed %s %s\n", pkg.Name, pkg.Version)
			installed++
		} else {
			fmt.Printf("Already in sync: %s %s\n", pkg.Name, pkg.Version)
			current++
		}
	}
	fmt.Printf("Sync complete: %d installed/updated, %d already in sync.\n", installed, current)
	return nil
}

// resolveForLockfile stores a portable package definition, including every
// supported release artifact. One-off GitHub installs intentionally keep their
// faster, current-platform-only resolution path.
func resolveForLockfile(target string) (registry.Package, error) {
	if strings.HasPrefix(strings.ToLower(target), "github:") {
		repo := strings.TrimSpace(target[len("github:"):])
		if repo == "" {
			return registry.Package{}, fmt.Errorf("usage: prl add github:owner/repo")
		}
		fmt.Printf("Inspecting GitHub release for a portable lock entry: %s\n", repo)
		draft, err := publisher.New().Generate(repo, "")
		if err != nil {
			return registry.Package{}, err
		}
		for _, warning := range draft.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		if !draft.Complete {
			return registry.Package{}, fmt.Errorf("release metadata could not be resolved safely across supported platforms; use 'prl install github:%s' for a platform-specific one-off install", repo)
		}
		if err := registry.ValidatePackage(draft.Package); err != nil {
			return registry.Package{}, fmt.Errorf("validate package metadata: %w", err)
		}
		if err := registry.ValidateGitHubProvenance(draft.Package); err != nil {
			return registry.Package{}, err
		}
		return draft.Package, nil
	}

	refreshRegistry()
	return registry.Find(target)
}

// ensureInstalled installs the exact package definition supplied by the
// lockfile. It never re-resolves a locked package against the live registry.
func ensureInstalled(pkg registry.Package) (bool, error) {
	current, err := install.IsCurrent(pkg)
	if err != nil {
		return false, err
	}
	records, err := install.RecordsFor(pkg.Name)
	if err != nil {
		return false, err
	}
	if current && len(records) == 1 {
		return false, nil
	}

	hasOtherVersion := len(records) > 1
	for _, record := range records {
		if record.Version != pkg.Version {
			hasOtherVersion = true
			break
		}
	}
	if len(records) > 0 && hasOtherVersion {
		_, _, err := install.Update(pkg)
		return true, err
	}
	if err := install.Install(pkg); err != nil {
		return false, err
	}
	return true, nil
}

func updateCommand(args []string) error {
	refreshRegistry()
	if len(args) > 1 {
		return fmt.Errorf("usage: prl update [package]")
	}
	if len(args) == 1 {
		return updateOne(args[0])
	}

	records, err := install.InstalledRecords()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("No packages installed.")
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, record := range records {
		key := strings.ToLower(record.Name)
		if !seen[key] {
			seen[key] = true
			names = append(names, record.Name)
		}
	}
	sort.Strings(names)
	var failures []string
	for _, name := range names {
		if err := updateOne(name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", name, err)
			failures = append(failures, name)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to update: %s", strings.Join(failures, ", "))
	}
	return nil
}

func updateOne(name string) error {
	pkg, err := registry.Find(name)
	if err != nil {
		return err
	}
	from, changed, err := install.Update(pkg)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Printf("%s is already up to date (%s).\n", pkg.Name, pkg.Version)
		return nil
	}
	fmt.Printf("Updated %s %s -> %s\n", pkg.Name, strings.Join(from, ","), pkg.Version)
	return nil
}

func packageCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: prl package inspect|generate|add|submit <github-repo>")
	}
	client := publisher.New()

	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return fmt.Errorf("usage: prl package inspect <github-repo>")
		}
		inspection, err := client.Inspect(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Repository: %s/%s\n", inspection.Owner, inspection.Repo)
		fmt.Printf("Release:    %s\n", inspection.Release)
		fmt.Printf("Version:    %s\n", inspection.Version)
		fmt.Printf("Assets:     %d supported\n", len(inspection.Assets))
		for _, asset := range inspection.Assets {
			fmt.Printf("  %-16s %-4s %s\n", asset.Platform, asset.Format, asset.Name)
		}
		for _, warning := range inspection.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		return nil

	case "generate", "add", "submit":
		repo := args[1]
		command := ""
		legacyRegistryPath := ""
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--command":
				if i+1 >= len(args) {
					return fmt.Errorf("--command requires a value")
				}
				command = args[i+1]
				i++
			case "--registry":
				if args[0] != "add" {
					return fmt.Errorf("--registry is only supported by 'prl package add'")
				}
				if i+1 >= len(args) {
					return fmt.Errorf("--registry requires a path")
				}
				legacyRegistryPath = args[i+1]
				i++
			default:
				return fmt.Errorf("unknown package option %q", args[i])
			}
		}

		draft, err := client.Generate(repo, command)
		if err != nil {
			return err
		}
		for _, warning := range draft.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		if args[0] == "generate" {
			data, err := encodePackageJSON(draft.Package)
			if err != nil {
				return fmt.Errorf("encode package draft: %w", err)
			}
			fmt.Print(string(data))
			if !draft.Complete {
				fmt.Fprintln(os.Stderr, "warning: generated draft is incomplete; review command and entrypoints before publishing it")
			}
			return nil
		}
		if !draft.Complete {
			return fmt.Errorf("package draft is incomplete; fix the warnings or provide --command before publishing it")
		}
		if err := registry.ValidateGitHubProvenance(draft.Package); err != nil {
			return err
		}

		if args[0] == "submit" {
			submitter, err := submit.New()
			if err != nil {
				return err
			}
			fmt.Printf("Submitting %s %s to the Packrail registry...\n", draft.Package.Name, draft.Package.Version)
			result, err := submitter.Submit(draft.Package)
			if err != nil {
				return err
			}
			fmt.Printf("Created registry PR #%d: %s\n", result.Number, result.URL)
			fmt.Printf("Branch: %s (%s)\n", result.Branch, result.Repository)
			return nil
		}

		if legacyRegistryPath != "" {
			replaced, err := publisher.UpsertRegistryFile(legacyRegistryPath, draft.Package)
			if err != nil {
				return err
			}
			action := "Added"
			if replaced {
				action = "Updated"
			}
			fmt.Printf("%s %s %s in legacy registry %s\n", action, draft.Package.Name, draft.Package.Version, legacyRegistryPath)
			return nil
		}

		manifestPath := registry.ManifestPath(canonicalManifestDir, draft.Package.Name)
		_, statErr := os.Stat(manifestPath)
		replaced := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if replaced {
			oldPkg, err := registry.ReadManifest(manifestPath)
			if err != nil {
				return err
			}
			if err := registry.ValidateMutation(oldPkg, draft.Package); err != nil {
				return err
			}
		}
		writtenPath, err := registry.WriteManifest(canonicalManifestDir, draft.Package)
		if err != nil {
			return err
		}
		count, err := registry.BuildIndex(canonicalManifestDir, canonicalIndexPath)
		if err != nil {
			return err
		}
		action := "Added"
		if replaced {
			action = "Updated"
		}
		fmt.Printf("%s %s %s in %s\n", action, draft.Package.Name, draft.Package.Version, writtenPath)
		fmt.Printf("Rebuilt %s (%d packages).\n", canonicalIndexPath, count)
		fmt.Println("Review the manifest and index diff, run 'prl registry validate --online', then commit.")
		return nil
	default:
		return fmt.Errorf("unknown package command %q; use inspect, generate, add, or submit", args[0])
	}
}

func runtimeCommand(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		if len(args) > 1 {
			return fmt.Errorf("usage: prl runtime list")
		}
		home, err := install.Home()
		if err != nil {
			return err
		}
		for _, name := range []string{"node", "python", "java", "dotnet"} {
			versions, err := packruntimes.InstalledVersions(home, name)
			if err != nil {
				return err
			}
			if len(versions) == 0 {
				fmt.Printf("%-8s not installed\n", name)
			} else {
				fmt.Printf("%-8s %s\n", name, strings.Join(versions, ", "))
			}
		}
		return nil
	}
	if args[0] != "install" || len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("usage: prl runtime install <node|python|java|dotnet> [constraint]")
	}
	name := strings.ToLower(args[1])
	defaults := map[string]string{"node": ">=20", "python": ">=3.11", "java": ">=21", "dotnet": ">=8"}
	constraint := defaults[name]
	if constraint == "" {
		return fmt.Errorf("unsupported runtime %q", name)
	}
	if len(args) == 3 {
		constraint = args[2]
	}
	home, err := install.Home()
	if err != nil {
		return err
	}
	fmt.Printf("Resolving %s %s for %s/%s...\n", name, constraint, runtime.GOOS, runtime.GOARCH)
	result, err := packruntimes.Ensure(home, registry.Runtime{Name: name, Constraint: constraint, Command: name})
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s %s\n", name, result.Version)
	fmt.Printf("Executable: %s\n", result.Executable)
	return nil
}

func encodePackageJSON(pkg registry.Package) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pkg); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func registryCommand(args []string) error {
	if len(args) > 0 && args[0] == "build" {
		return registryBuild(args[1:])
	}
	if len(args) > 0 && args[0] == "validate" {
		return registryValidate(args[1:])
	}
	if len(args) == 0 || args[0] == "status" {
		if len(args) > 1 {
			return fmt.Errorf("usage: prl registry status")
		}
		status, err := registry.Status()
		if err != nil {
			return err
		}
		printRegistryStatus(status)
		return nil
	}

	if args[0] == "sync" {
		if len(args) != 1 {
			return fmt.Errorf("usage: prl registry sync")
		}
		fmt.Println("Syncing registry from GitHub...")
		status, err := registry.Sync()
		if err != nil {
			return err
		}
		fmt.Printf("Synced %d packages.\n", status.PackageCount)
		fmt.Printf("Cache: %s\n", status.CachePath)
		return nil
	}

	return fmt.Errorf("unknown registry command %q; use status, sync, build, or validate", args[0])
}

func printRegistryStatus(status registry.StatusInfo) {
	fmt.Printf("Registry source:   %s\n", status.Source)
	fmt.Printf("Registry URL:      %s\n", status.URL)
	fmt.Printf("Packages:          %d\n", status.PackageCount)
	fmt.Printf("Cache:             %s\n", status.CachePath)
	if !status.UpdatedAt.IsZero() {
		fmt.Printf("Cache updated:     %s\n", status.UpdatedAt.UTC().Format(time.RFC3339))
		fmt.Printf("Cache age:         %s\n", status.Age.Round(time.Second))
	}
}

func refreshRegistry() {
	if err := registry.RefreshIfStale(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: registry refresh failed; using last valid local registry: %v\n", err)
	}
}

func listInstalled() error {
	records, err := install.InstalledRecords()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("No packages installed.")
		return nil
	}
	for _, record := range records {
		fmt.Printf("%-20s %s\n", record.Name, record.Version)
	}
	return nil
}

func doctor() error {
	home, err := install.Home()
	if err != nil {
		return err
	}
	binDir := filepath.Join(home, "bin")
	fmt.Printf("Packrail %s\n", version)
	fmt.Printf("OS/Arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Home:     %s\n", home)
	fmt.Printf("Bin:      %s\n", binDir)

	if pathContains(binDir) {
		fmt.Println("PATH:     ok")
	} else {
		fmt.Println("PATH:     Packrail bin directory is not on PATH")
	}

	status, err := registry.Status()
	if err == nil {
		fmt.Printf("Registry: %s (%d packages)\n", status.Source, status.PackageCount)
	}
	for _, name := range []string{"node", "python", "java", "dotnet"} {
		versions, err := packruntimes.InstalledVersions(home, name)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			fmt.Printf("%-9s no managed runtime installed\n", strings.Title(name)+":")
		} else {
			fmt.Printf("%-9s managed %s\n", strings.Title(name)+":", strings.Join(versions, ", "))
		}
	}
	return nil
}

func pathContains(target string) bool {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		entryAbs, err := filepath.Abs(entry)
		if err == nil && strings.EqualFold(filepath.Clean(entryAbs), filepath.Clean(targetAbs)) {
			return true
		}
	}
	return false
}

func printHelp() {
	fmt.Printf(`Packrail %s
Universal package distribution for open-source software.

Usage:
  prl install <package>                         Install a registry package and required runtime
  prl install github:owner/repo                 Install directly from a GitHub Release
  prl add <package|github:owner/repo>            Install and record a project tool
  prl sync                                       Install packages pinned in packrail.lock
  prl update [package]                          Update one package or all installed packages
  prl remove <package>                          Remove a package and its tracked shims/files
  prl search [query]                            Search the GitHub-backed registry
  prl info <package>                            Show package and platform metadata
  prl package inspect <github-repo>             Inspect a GitHub Release for Packrail assets
  prl package generate <github-repo>            Generate a Packrail registry draft
  prl package add <github-repo>                 Generate and write a local registry manifest
  prl package submit <github-repo>              Generate, verify ownership, and open a registry PR
  prl runtime install <runtime> [constraint]    Install a managed Node/Python/Java/.NET runtime
  prl runtime list                              List Packrail-managed runtimes
  prl registry status                           Show registry source and cache state
  prl registry sync                             Force-sync package metadata from GitHub
  prl registry build                            Rebuild registry/index.json from package manifests
  prl registry validate [--online]              Validate manifests, index, provenance, and URLs
  prl list                                      List installed packages
  prl doctor                                    Check the local Packrail environment
  prl version                                   Print the CLI version
  prl help                                      Show this help
`, version)
}
