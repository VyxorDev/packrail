package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/install"
	"github.com/VyxorDev/packrail/internal/publisher"
	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
)

const version = "0.5.0-alpha"

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
			return fmt.Errorf("usage: prl install <package>")
		}
		refreshRegistry()
		pkg, err := registry.Find(args[1])
		if err != nil {
			return err
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

	case "list", "ls":
		return listInstalled()

	case "doctor":
		return doctor()

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

func packageCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: prl package inspect <github-repo> | prl package generate <github-repo> [--command <name>]")
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

	case "generate":
		repo := args[1]
		command := ""
		for i := 2; i < len(args); i++ {
			if args[i] != "--command" || i+1 >= len(args) {
				return fmt.Errorf("usage: prl package generate <github-repo> [--command <name>]")
			}
			command = args[i+1]
			i++
		}
		draft, err := client.Generate(repo, command)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(draft.Package, "", "  ")
		if err != nil {
			return fmt.Errorf("encode package draft: %w", err)
		}
		fmt.Println(string(data))
		for _, warning := range draft.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		if !draft.Complete {
			fmt.Fprintln(os.Stderr, "warning: generated draft is incomplete; review command and entrypoints before adding it to the registry")
		}
		return nil
	default:
		return fmt.Errorf("unknown package command %q; use 'prl package inspect' or 'prl package generate'", args[0])
	}
}

func registryCommand(args []string) error {
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

	return fmt.Errorf("unknown registry command %q; use 'prl registry status' or 'prl registry sync'", args[0])
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
	home, err := install.Home()
	if err != nil {
		return err
	}
	packagesDir := filepath.Join(home, "packages")
	entries, err := os.ReadDir(packagesDir)
	if os.IsNotExist(err) {
		fmt.Println("No packages installed.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read installed packages: %w", err)
	}

	type row struct{ name, version string }
	var rows []row
	for _, packageEntry := range entries {
		if !packageEntry.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(packagesDir, packageEntry.Name()))
		if err != nil {
			continue
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			recordPath := filepath.Join(packagesDir, packageEntry.Name(), versionEntry.Name(), ".packrail.json")
			data, err := os.ReadFile(recordPath)
			if err != nil {
				continue
			}
			var record install.InstallRecord
			if json.Unmarshal(data, &record) == nil {
				rows = append(rows, row{record.Name, record.Version})
			}
		}
	}
	if len(rows) == 0 {
		fmt.Println("No packages installed.")
		return nil
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].name == rows[j].name {
			return rows[i].version < rows[j].version
		}
		return rows[i].name < rows[j].name
	})
	for _, r := range rows {
		fmt.Printf("%-20s %s\n", r.name, r.version)
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

	nodeVersions, err := packruntimes.InstalledVersions(home, "node")
	if err != nil {
		return err
	}
	if len(nodeVersions) == 0 {
		fmt.Println("Node:     no Packrail-managed runtime installed")
	} else {
		fmt.Printf("Node:     managed %s\n", strings.Join(nodeVersions, ", "))
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
  prl install <package>                 Install a package and its required runtime
  prl search [query]                    Search the GitHub-backed registry
  prl info <package>                    Show package and platform metadata
  prl package inspect <github-repo>     Inspect a GitHub Release for Packrail assets
  prl package generate <github-repo>    Generate a Packrail registry draft
  prl registry status                   Show registry source and cache state
  prl registry sync                     Force-sync package metadata from GitHub
  prl list                              List installed packages
  prl doctor                            Check the local Packrail environment
  prl version                           Print the CLI version
  prl help                              Show this help
`, version)
}
