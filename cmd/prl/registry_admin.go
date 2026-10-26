package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

const canonicalManifestDir = "registry/packages"
const canonicalIndexPath = "registry/index.json"

func registryBuild(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: prl registry build")
	}
	count, err := registry.BuildIndex(canonicalManifestDir, canonicalIndexPath)
	if err != nil {
		return err
	}
	fmt.Printf("Built %s from %d package manifests.\n", canonicalIndexPath, count)
	return nil
}

func registryValidate(args []string) error {
	online := false
	baseRef := ""
	submitter := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--online":
			online = true
		case "--base":
			if i+1 >= len(args) {
				return fmt.Errorf("--base requires a git ref")
			}
			baseRef = args[i+1]
			i++
		case "--submitter":
			if i+1 >= len(args) {
				return fmt.Errorf("--submitter requires a GitHub login")
			}
			submitter = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown registry validate option %q", args[i])
		}
	}
	if submitter != "" && baseRef == "" {
		return fmt.Errorf("--submitter requires --base so Packrail can identify changed packages")
	}

	count, err := registry.ValidateIndex(canonicalManifestDir, canonicalIndexPath)
	if err != nil {
		return err
	}
	packages, err := registry.LoadCatalog(canonicalManifestDir)
	if err != nil {
		return err
	}
	for _, pkg := range packages {
		if err := registry.ValidateGitHubProvenance(pkg); err != nil {
			return err
		}
	}

	verifyPackages := packages
	if baseRef != "" {
		if err := validateRegistryHistory(baseRef); err != nil {
			return err
		}
		changed, err := changedRegistryPackages(baseRef)
		if err != nil {
			return err
		}
		verifyPackages = changed
	}

	if online {
		for _, pkg := range verifyPackages {
			fmt.Printf("Verifying %-20s %s...\n", pkg.Name, pkg.Version)
			if err := registry.VerifyPackageOnline(pkg); err != nil {
				return fmt.Errorf("verify %s: %w", pkg.Name, err)
			}
		}
	}
	if submitter != "" {
		token := firstEnv("PACKRAIL_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN")
		for _, pkg := range verifyPackages {
			fmt.Printf("Verifying publisher @%s for %s...\n", submitter, pkg.Name)
			if err := registry.VerifyPublisherOnline(pkg, submitter, token); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Registry valid: %d packages.\n", count)
	return nil
}

func validateRegistryHistory(baseRef string) error {
	paths, err := gitLines("ls-tree", "-r", "--name-only", baseRef, "--", canonicalManifestDir)
	if err != nil {
		return fmt.Errorf("list base registry manifests: %w", err)
	}
	for _, oldPath := range paths {
		if strings.TrimSpace(oldPath) == "" || !strings.HasSuffix(oldPath, ".json") {
			continue
		}
		currentPath := filepath.FromSlash(oldPath)
		if _, err := os.Stat(currentPath); os.IsNotExist(err) {
			return fmt.Errorf("registry package deletion is not allowed in a submission: %s", oldPath)
		} else if err != nil {
			return err
		}

		oldData, err := gitOutput("show", baseRef+":"+oldPath)
		if err != nil {
			return fmt.Errorf("read %s from %s: %w", oldPath, baseRef, err)
		}
		var oldPkg registry.Package
		if err := json.Unmarshal(oldData, &oldPkg); err != nil {
			return fmt.Errorf("decode base manifest %s: %w", oldPath, err)
		}
		if err := registry.ValidatePackage(oldPkg); err != nil {
			return fmt.Errorf("validate base manifest %s: %w", oldPath, err)
		}
		newPkg, err := registry.ReadManifest(currentPath)
		if err != nil {
			return err
		}
		if err := registry.ValidateMutation(oldPkg, newPkg); err != nil {
			return err
		}
	}
	return nil
}

func changedRegistryPackages(baseRef string) ([]registry.Package, error) {
	paths, err := gitLines("diff", "--name-only", baseRef+"...HEAD", "--", canonicalManifestDir)
	if err != nil {
		return nil, fmt.Errorf("list changed registry manifests: %w", err)
	}
	var packages []registry.Package
	for _, changedPath := range paths {
		if !strings.HasSuffix(changedPath, ".json") {
			continue
		}
		localPath := filepath.FromSlash(changedPath)
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			continue // deletion is rejected separately by validateRegistryHistory
		} else if err != nil {
			return nil, err
		}
		pkg, err := registry.ReadManifest(localPath)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func gitLines(args ...string) ([]string, error) {
	data, err := gitOutput(args...)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func gitOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
