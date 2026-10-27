package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/VyxorDev/packrail/internal/install"
	"github.com/VyxorDev/packrail/internal/manifest"
	"github.com/VyxorDev/packrail/internal/project"
)

func projectInitCommand(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: prl init")
	}
	manager, err := project.New("")
	if err != nil { return err }
	result, err := manager.Init()
	if err != nil { return err }
	if !result.Created {
		fmt.Printf("Project already initialized: %s (%d dependencies).\n", result.Path, result.Dependencies)
		return nil
	}
	if result.MigratedLock {
		fmt.Printf("Created %s from the existing lockfile with %d exact dependency pins.\n", result.Path, result.Dependencies)
		fmt.Println("No loose version ranges or direct-source intent were invented during migration.")
		return nil
	}
	fmt.Printf("Created %s.\n", result.Path)
	return nil
}

func projectAddCommand(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: prl add <package[@constraint]|github:owner/repo[@constraint]>")
	}
	manager, err := project.New("")
	if err != nil { return err }
	result, err := manager.Add(args[0])
	if err != nil { return err }
	if result.Installed {
		fmt.Printf("Added and installed %s %s.\n", result.Package.Name, result.Package.Version)
	} else {
		fmt.Printf("Added %s %s; the exact package is already installed.\n", result.Package.Name, result.Package.Version)
	}
	fmt.Printf("Intent: %s\nResolved state: packrail.lock\n", manifest.FileName)
	return nil
}

func projectSyncCommand(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: prl sync")
	}
	manager, err := project.New("")
	if err != nil { return err }
	result, err := manager.Sync()
	if err != nil { return err }
	if result.Packages == 0 {
		fmt.Println("Nothing to sync.")
		return nil
	}
	if result.LockOnly {
		fmt.Println("Syncing legacy lockfile-only project (v0.9 compatibility mode).")
	}
	fmt.Printf("Sync complete: %d installed/updated, %d already in sync.\n", result.Installed, result.Current)
	return nil
}

func projectUpdateCommand(args []string) error {
	if !projectManifestExists() {
		return updateCommand(args)
	}
	manager, err := project.New("")
	if err != nil { return err }
	result, err := manager.Update(args)
	if err != nil { return err }
	if len(result.Packages) == 0 {
		fmt.Println("No project dependencies to update.")
		return nil
	}
	for _, pkg := range result.Packages {
		fmt.Printf("Resolved %s -> %s\n", pkg.Name, pkg.Version)
	}
	fmt.Printf("Updated packrail.lock for %d project dependencies; packrail.json intent was unchanged.\n", len(result.Packages))
	return nil
}

func projectRemoveCommand(args []string) error {
	uninstall := false
	name := ""
	for _, arg := range args {
		if arg == "--uninstall" {
			uninstall = true
			continue
		}
		if name != "" {
			return fmt.Errorf("usage: prl remove <package> [--uninstall]")
		}
		name = strings.TrimSpace(arg)
	}
	if name == "" {
		return fmt.Errorf("usage: prl remove <package> [--uninstall]")
	}

	if projectManifestExists() {
		manager, err := project.New("")
		if err != nil { return err }
		result, err := manager.Remove(name, uninstall)
		if err != nil { return err }
		fmt.Printf("Removed %s from the project manifest and lockfile.\n", result.Name)
		if result.Uninstalled {
			fmt.Println("The shared/global installation was also removed because --uninstall was explicitly requested.")
		} else {
			fmt.Println("The shared/global installation was left untouched.")
		}
		return nil
	}

	removed, err := install.Remove(name)
	if err != nil { return err }
	versions := make([]string, 0, len(removed))
	for _, record := range removed { versions = append(versions, record.Version) }
	fmt.Printf("Removed %s (%s)\n", removed[0].Name, strings.Join(versions, ", "))
	fmt.Println("Shared runtime caches were kept for other packages.")
	return nil
}

func projectManifestExists() bool {
	info, err := os.Stat(manifest.FileName)
	return err == nil && !info.IsDir()
}
