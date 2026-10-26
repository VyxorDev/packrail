package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/install"
	"github.com/VyxorDev/packrail/internal/registry"
)

const version = "0.1.0-alpha"

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
		pkg, err := registry.Find(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Resolving %s %s\n", pkg.Name, pkg.Version)
		fmt.Printf("Source: %s\n", pkg.Source)
		fmt.Println("Downloading and verifying artifact...")
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
		pkg, err := registry.Find(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", pkg.Name, pkg.Version)
		fmt.Println(pkg.Description)
		fmt.Printf("Source:  %s\n", pkg.Source)
		fmt.Printf("Runtime: %s %s\n", pkg.Runtime.Name, pkg.Runtime.Constraint)
		fmt.Printf("Command: %s\n", pkg.Command.Name)
		fmt.Printf("SHA-256: %s\n", pkg.Artifact.SHA256)
		return nil

	case "search", "s":
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

	pathOK := pathContains(binDir)
	if pathOK {
		fmt.Println("PATH:     ok")
	} else {
		fmt.Println("PATH:     Packrail bin directory is not on PATH")
	}

	if node, err := exec.LookPath("node"); err == nil {
		fmt.Printf("Node:     %s\n", node)
	} else {
		fmt.Println("Node:     not found (needed by StarterQueue alpha package)")
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
  prl install <package>   Install a package
  prl search [query]      Search the registry
  prl info <package>      Show package metadata
  prl list                List installed packages
  prl doctor              Check the local Packrail environment
  prl version             Print the CLI version
  prl help                Show this help
`, version)
}
